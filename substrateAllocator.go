package main

import (
	"errors"
	"fmt"
	"math"
	"math/bits"

	"github.com/tetratelabs/wazero/api"
)

const (
	MaxPossibleAlloc        = (1 << 25)
	MinPossibleAlloc        = 8
	HeaderSize              = 8
	NumOrders               = 23
	MaxWasmPages     uint32 = 4 * 1024 * 1024 * 1024 / PageSize
)

type Order uint32

func (o Order) size() uint32 {
	return MinPossibleAlloc << o
}

func (o Order) intoRaw() uint32 {
	return uint32(o)
}

func orderFromRaw(order uint32) (Order, error) {
	if order < NumOrders {
		return Order(order), nil
	}
	return Order(0), errors.New("invalid order")
}

func orderFromSize(size uint32) (Order, error) {
	if size > MaxPossibleAlloc {
		return Order(0), errors.New("requested allocation too large")
	}

	if size < MinPossibleAlloc {
		size = MinPossibleAlloc
	}

	powerOfTwoSize := nextPowerOf2GT8(size)
	value := bits.TrailingZeros32(powerOfTwoSize) - bits.TrailingZeros32(MinPossibleAlloc)
	return Order(value), nil
}

const NilMarker = math.MaxUint32

// A link between headers in the free list.
type Link interface {
	isLink()
	intoRaw() uint32
}

type Nil struct{}

func (Nil) isLink() {}
func (Nil) intoRaw() uint32 {
	return NilMarker
}

type Ptr struct {
	headerPtr uint32
}

func (Ptr) isLink() {}
func (p Ptr) intoRaw() uint32 {
	return p.headerPtr
}

var _ Link = (*Nil)(nil)
var _ Link = (*Ptr)(nil)

func linkFromRaw(raw uint32) Link {
	if raw != NilMarker {
		return Ptr{headerPtr: raw}
	}
	return Nil{}
}

type Header interface {
	isHeader()
	intoOccupied() (Order, bool)
	intoFree() (Link, bool)
}

type Free struct {
	link Link
}

func (Free) isHeader() {}
func (f Free) intoOccupied() (Order, bool) {
	return Order(0), false
}
func (f Free) intoFree() (Link, bool) {
	return f.link, true
}

type Occupied struct {
	order Order
}

func (Occupied) isHeader() {}
func (f Occupied) intoOccupied() (Order, bool) {
	return f.order, true
}
func (f Occupied) intoFree() (Link, bool) {
	return nil, false
}

var _ Header = (*Free)(nil)
var _ Header = (*Occupied)(nil)

// headerFromMemory reads a header from memory, returns an error
// if ther `header_ptr` is out of bounds of the linear memory
// or if the read header is corrupted (e.g order is incorrect)
func headerFromMemory(memory api.Memory, header_ptr uint32) (Header, error) {
	rawHeader, ok := memory.ReadUint64Le(header_ptr)
	if !ok {
		return nil, errors.New("failed to read header")
	}

	// check if the header represents an occupied or free allocation
	// and extract the header data by timing (and discarding) the high bits
	occupied := rawHeader&0x00000001_00000000 != 0
	headerData := uint32(rawHeader)

	if occupied {
		order, err := orderFromRaw(headerData)
		if err != nil {
			return nil, fmt.Errorf("order from raw: %w", err)
		}
		return Occupied{order}, nil
	}

	return Free{link: linkFromRaw(headerData)}, nil
}

func headerWriteInto(header Header, mem api.Memory, headerPtr uint32) error {
	var (
		headerData   uint64
		occupiedMask uint64
	)

	switch v := header.(type) {
	case Occupied:
		headerData = uint64(v.order.intoRaw())
		occupiedMask = 0x00000001_00000000
	case Free:
		headerData = uint64(v.link.intoRaw())
		occupiedMask = 0x00000000_00000000
	default:
		panic(fmt.Sprintf("header type %T not supported", header))
	}

	fmt.Printf("header data: %d\n\toccupied mask: %x\n\t", headerData, occupiedMask)
	rawHeader := headerData | occupiedMask
	fmt.Printf("raw header: %x\n", rawHeader)
	ok := mem.WriteUint64Le(headerPtr, rawHeader)
	if !ok {
		return errors.New("failed to write raw header")
	}
	return nil
}

type freeLists struct {
	heads [NumOrders]Link
}

func newFreeLists() *freeLists {
	free := [NumOrders]Link{}
	for idx := 0; idx < NumOrders; idx++ {
		free[idx] = Nil{}
	}

	return &freeLists{
		heads: free,
	}
}

// replace replaces a given link for the specified order and returns the old one
func (f *freeLists) replace(order Order, new Link) (old Link) {
	prev := f.heads[order]
	f.heads[order] = new
	return prev
}

type substrateAllocator struct {
	originalHeapBase    uint32
	bumper              uint32
	freeLists           *freeLists
	poisoned            bool
	lastObservedMemSize uint64
}

func NewSubstrateAllocator(heapBase uint32) *substrateAllocator {
	alignedHeapBase := (heapBase + alignment - 1) / alignment * alignment
	return &substrateAllocator{
		originalHeapBase:    alignedHeapBase,
		bumper:              alignedHeapBase,
		freeLists:           newFreeLists(),
		poisoned:            false,
		lastObservedMemSize: 0,
	}
}

func (s *substrateAllocator) Allocate(mem api.Memory, size uint32) (uint32, error) {
	if s.poisoned {
		return 0, errors.New("the allocator has been poisoned")
	}

	// TODO: discuss about PoisonBomb, if the PoisonBomb is dropped
	// before being disarmed then the allocator will be considered poisoned

	order, err := orderFromSize(size)
	if err != nil {
		return 0, fmt.Errorf("order from size: %w", err)
	}

	fmt.Printf("order: %d\n", order)

	var headerPtr uint32
	link := s.freeLists.heads[order]
	switch v := link.(type) {
	case Ptr:
		if v.headerPtr+order.size()+uint32(HeaderSize) > mem.Size() {
			return 0, errors.New("invalid header pointer detected")
		}

		header, err := headerFromMemory(mem, v.headerPtr)
		if err != nil {
			return 0, fmt.Errorf("reading header from memory: %w", err)
		}

		nextFree, ok := header.intoFree()
		if !ok {
			return 0, errors.New("free list points to a occupied header")
		}

		s.freeLists.heads[order] = nextFree
		headerPtr = v.headerPtr
	case Nil:
		fmt.Printf("bumping:\n\tbumper: %d, order size: %d\n\t", s.bumper, order.size())
		// Corresponding free list is empty. Allocate a new item
		newPtr, err := bump(&s.bumper, order.size()+HeaderSize, mem)
		if err != nil {
			return 0, fmt.Errorf("bumpinp: %w", err)
		}
		fmt.Printf("ptr after bumping: %d\n", newPtr)
		headerPtr = newPtr
	default:
		panic(fmt.Sprintf("link type %T not supported", link))
	}

	fmt.Printf("header write into:\n\torder: %d\n\t", order)
	// Write the order in the occupied header
	err = headerWriteInto(Occupied{order}, mem, headerPtr)
	if err != nil {
		return 0, fmt.Errorf("write header into: %w", err)
	}

	// TODO: allocation stats update
	fmt.Printf("bytes allocated: %d\naddress space used:%d\n",
		order.size()+HeaderSize, s.bumper-s.originalHeapBase)
	fmt.Printf("returned pointer: %d\n", headerPtr+HeaderSize)
	return headerPtr + HeaderSize, nil
}

func bump(bumper *uint32, size uint32, mem api.Memory) (uint32, error) {
	requiredSize := *bumper + size
	fmt.Printf("required size: %d\n\t", requiredSize)
	fmt.Printf("current memory size: %d\n\t", mem.Size())
	if requiredSize > mem.Size() {
		requiredPages, ok := pagesFromSize(requiredSize)
		if !ok {
			return 0, errors.New("allocator out of space")
		}

		fmt.Printf("required pages: %d\n\t", requiredPages)

		currentPages := mem.Size() / PageSize
		if currentPages >= requiredPages {
			panic(fmt.Sprintf("current pages %d should be less than required pages %d", currentPages, requiredPages))
		}

		fmt.Printf("current pages: %d\n\tmax wasm pages: %d\n\t", currentPages, MaxWasmPages)

		if currentPages >= MaxWasmPages {
			return 0, fmt.Errorf("allocator out of space: current pages %d greater than max wasm pages %d", currentPages, MaxWasmPages)
		}

		if requiredPages > MaxWasmPages {
			return 0, fmt.Errorf("allocator out of space: required pages %d greater than max wasm pages %d", requiredPages, MaxWasmPages)
		}

		// ideally we want to double our current number of pages,
		// as long as it's less than the double absolute max we can have
		nextPages := min(currentPages*2, MaxWasmPages)
		fmt.Printf("(min) next pages: %d\n\t", nextPages)
		// ... but if even more pages are required then try to allocate that many
		nextPages = max(nextPages, requiredPages)
		fmt.Printf("(max) next pages: %d\n\t", nextPages)
		fmt.Printf("grow memory parameter: %d\n\t", nextPages-currentPages)

		_, ok = mem.Grow(nextPages - currentPages)
		if !ok {
			return 0, fmt.Errorf("failed to grow from %d pages to %d pages", currentPages, nextPages)
		}

		fmt.Printf("mem.Size() after grow: %d\n\t", mem.Size())
		pagesIncrease := (mem.Size() / PageSize) == nextPages
		if !pagesIncrease {
			panic(fmt.Sprintf("Number of pages should have increased! Previous: %d, Desired: %d", currentPages, nextPages))
		}
	}

	res := *bumper
	*bumper += size
	return res, nil
}

// pagesFromSize convert the given `size` in bytes into the number of pages.
// The returned number of pages is ensured to be big enough to hold memory
// with the given `size`.
// Returns false if the number of pages do not fit into `u32`
func pagesFromSize(size uint32) (uint32, bool) {
	sizePlusPageSize, ok := checkSaturationSum(size, PageSize-1)
	if !ok {
		return 0, false
	}

	return uint32(sizePlusPageSize / PageSize), true
}

func checkSaturationSum(a, b uint32) (uint32, bool) {
	max := ^uint32(0)
	if a > max-b {
		return max, false
	}
	return a + b, true
}
