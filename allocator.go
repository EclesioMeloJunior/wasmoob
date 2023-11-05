package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"

	"github.com/tetratelabs/wazero/api"
)

// The pointers need to be aligned to 8 bytes
const alignment uint32 = 8

// HeadsQty 23
const HeadsQty = 23

// MaxPossibleAllocation 2^25 bytes, 32 MiB
const MaxPossibleAllocation = (1 << 25)

const PageSize = 65536

type freeingBumpHeapAllocator struct {
	bumper      uint32
	heads       [HeadsQty]uint32
	heap        api.Memory
	maxHeapSize uint32
	ptrOffset   uint32
	totalSize   uint32
}

func NewAllocator(mem api.Memory, hb uint32) *freeingBumpHeapAllocator {
	fbha := new(freeingBumpHeapAllocator)

	padding := hb % alignment
	if padding != 0 {
		hb += alignment - padding
	}

	if mem.Size() <= hb {
		_, ok := mem.Grow(((hb - mem.Size()) / PageSize) + 1)
		if !ok {
			panic("exceeds max memory definition")
		}
	}

	fbha.bumper = 0
	fbha.heap = mem
	fbha.maxHeapSize = mem.Size() - alignment
	fbha.ptrOffset = hb
	fbha.totalSize = 0

	return fbha
}

// Allocate determines if there is space available in WASM heap to grow the heap by 'size'.  If there is space
// available it grows the heap to fit give 'size'.  The heap grows is chunks of Powers of 2, so the growth becomes
// the next highest power of 2 of the requested size.
func (fbha *freeingBumpHeapAllocator) Allocate(size uint32) (uint32, error) {
	fmt.Printf("calling Allocate with size: %d\n", size)

	// test for space allocation
	if size > MaxPossibleAllocation {
		err := errors.New("size too large")
		return 0, err
	}
	itemSize := nextPowerOf2GT8(size)
	fmt.Printf("next power of 2 of %d: %d\n", size, itemSize)

	if (itemSize + fbha.totalSize + fbha.ptrOffset) > fbha.maxHeapSize {
		pagesNeeded := ((itemSize + fbha.totalSize + fbha.ptrOffset) - fbha.maxHeapSize) / PageSize
		err := fbha.growHeap(pagesNeeded + 1)
		if err != nil {
			return 0, fmt.Errorf("allocator out of space; failed to grow heap; %w", err)
		}
	}

	// get pointer based on list_index
	listIndex := bits.TrailingZeros32(itemSize) - 3

	var ptr uint32
	if item := fbha.heads[listIndex]; item != 0 {
		// Something from the free list
		fourBytes := fbha.getHeap4bytes(item)
		fbha.heads[listIndex] = binary.LittleEndian.Uint32(fourBytes)
		ptr = item + 8
	} else {
		// Nothing te be freed. Bump.
		ptr = fbha.bump(itemSize+8) + 8
		fmt.Printf("bumping: ptr = %d\n", ptr)
	}

	fmt.Printf("fbha.maxHeapSize: %d\n", fbha.maxHeapSize)

	if (ptr + itemSize + fbha.ptrOffset) > fbha.maxHeapSize {
		pagesNeeded := (ptr + itemSize + fbha.ptrOffset - fbha.maxHeapSize) / PageSize
		err := fbha.growHeap(pagesNeeded + 1)
		if err != nil {
			return 0, fmt.Errorf("allocator out of space; failed to grow heap; %w", err)
		}

		if fbha.maxHeapSize < (ptr + itemSize + fbha.ptrOffset) {
			panic(fmt.Sprintf("failed to grow heap, want %d have %d", (ptr + itemSize + fbha.ptrOffset), fbha.maxHeapSize))
		}
	}

	// write "header" for allocated memory to heap
	for i := uint32(1); i <= 8; i++ {
		fbha.setHeap(ptr-i, 255)
	}
	fbha.setHeap(ptr-8, uint8(listIndex))
	fbha.totalSize = fbha.totalSize + itemSize + 8

	fmt.Printf("heap_base: %d + ptr: %d = %d\n", fbha.ptrOffset, ptr, fbha.ptrOffset+ptr)

	return fbha.ptrOffset + ptr, nil
}

func (fbha *freeingBumpHeapAllocator) setHeap(ptr uint32, value uint8) {
	if !fbha.heap.WriteByte(fbha.ptrOffset+ptr, value) {
		panic("write: out of range")
	}
}

func (fbha *freeingBumpHeapAllocator) growHeap(numPages uint32) error {
	_, ok := fbha.heap.Grow(numPages)
	if !ok {
		return fmt.Errorf("heap.Grow ignored")
	}

	fbha.maxHeapSize = fbha.heap.Size() - alignment
	return nil
}

func (fbha *freeingBumpHeapAllocator) bump(qty uint32) uint32 {
	res := fbha.bumper
	fbha.bumper += qty
	return res
}

func (fbha *freeingBumpHeapAllocator) getHeap4bytes(ptr uint32) []byte {
	bytes, ok := fbha.heap.Read(fbha.ptrOffset+ptr, 4)
	if !ok {
		panic("read: out of range")
	}
	return bytes
}

func nextPowerOf2GT8(v uint32) uint32 {
	if v < 8 {
		return 8
	}
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v++
	return v
}
