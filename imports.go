package main

import (
	"context"

	"github.com/tetratelabs/wazero/api"
)

func ext_allocator_malloc_version_1(ctx context.Context, _ api.Module, size uint32) uint32 {
	manager := ctx.Value(runtimeContextKey).(*RuntimeManager)

	// using gossamer allocator
	// res, err := manager.allocator.Allocate(size)
	// if err != nil {
	// 	panic(err)
	// }

	// using substrate allocator
	res, err := manager.substrateAlloc.Allocate(manager.mod.Memory(), size)
	if err != nil {
		panic(err)
	}

	return res
}
