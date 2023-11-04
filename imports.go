package main

import (
	"context"

	"github.com/tetratelabs/wazero/api"
)

func ext_allocator_malloc_version_1(ctx context.Context, _ api.Module, size uint32) uint32 {
	allocator := ctx.Value(runtimeContextKey).(*RuntimeManager).allocator

	// Allocate memory
	res, err := allocator.Allocate(size)
	if err != nil {
		panic(err)
	}

	return res
}
