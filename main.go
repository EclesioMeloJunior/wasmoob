package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

const runtimeContextKey = "runtime::context"

//go:embed testdata/simple_out_of_bounds.wasm
var simpleOutOfBounds []byte

func main() {
	// Choose the context to use for function calls.
	ctx := context.Background()

	// Create a new WebAssembly Runtime.
	rt := wazero.NewRuntime(ctx)
	defer rt.Close(ctx) // This closes everything this Runtime created.

	_, err := rt.NewHostModuleBuilder("env").
		ExportMemory("memory", 1).
		NewFunctionBuilder().
		WithFunc(ext_allocator_malloc_version_1).
		Export("ext_allocator_malloc_version_1").
		Instantiate(ctx)
	if err != nil {
		panic(err)
	}

	// Instantiate WASI, which implements host functions needed for TinyGo to
	// implement `panic`.
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)

	// Instantiate the guest Wasm into the same runtime. It exports the `add`
	// function, implemented in WebAssembly.
	mod, err := rt.Instantiate(ctx, simpleOutOfBounds)
	if err != nil {
		log.Panicf("failed to instantiate module: %v", err)
	}

	global := mod.ExportedGlobal("__heap_base")
	if global == nil {
		panic("wazero error: nil global for __heap_base")
	}

	hb := api.DecodeU32(global.Get())
	// hb = runtime.DefaultHeapBase

	mem := mod.Memory()
	if mem == nil {
		panic("wazero error: nil memory for module")
	}

	fmt.Printf("mem: \n\theap base: %d\n\tmem size: %d\n", hb, mem.Size())

	substrateAlloc := NewSubstrateAllocator(hb)
	allocator := NewAllocator(mem, hb)

	manager := &RuntimeManager{mod, allocator, substrateAlloc}
	runtimeCtx := context.WithValue(context.Background(), runtimeContextKey, manager)

	// Call the `add` function and print the results to the console.
	oob := manager.mod.ExportedFunction("out_of_bounds")
	result, err := oob.Call(runtimeCtx)
	if err != nil {
		panic(err)
	}

	fmt.Println(result)

	mem = mod.Memory()
	if mem == nil {
		panic("wazero error: nil memory for module")
	}

	values := make([]uint32, 0)
	for i := uint32(result[0]); true; i += 4 {
		value, ok := mem.ReadUint32Le(i)
		if !ok {
			fmt.Printf("cannot read uint32_le, idx: %d\n", i)
			break
		}

		if value != 9090 {
			fmt.Printf("stopped at idx: %d\n", i)
			break
		}
		values = append(values, value)
	}

	fmt.Printf("total: %d\n", len(values))
}
