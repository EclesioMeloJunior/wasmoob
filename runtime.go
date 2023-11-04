package main

import "github.com/tetratelabs/wazero/api"

type RuntimeManager struct {
	mod       api.Module
	allocator *freeingBumpHeapAllocator
}
