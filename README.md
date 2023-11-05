## Wasmoob (Wasm Out of Bounds)

This projects tests Gossamer allocator in order to investigate a Wasm Out of Bounds problem

- To execute this project first run:

```
make wasm
```

and then

```
go run ./...
```

Basically this projects has a wasm logic that fills 1 page (65536 bytes) with i32 data (the number 9090) and then returns the offset. The returned value is basically the latest linear memory point. In the output you can see

```
[65536] <- latest memory offset, from this point nothing can be allocated anymore
idx: 65536 encoded bytes len: 0 <- golang max index it can decodes data
total: 16384 <- total of data stored in the page
```

This wasm exports the memory, which in turn is managed by an allocator, the next steps is: once we fill 1 page of data we ask the allocator 1 more page (more 65536 bytes), and we should check if the allocator is doing it right.

By introducing the following line after the for loop

````
... wasm code
(call $ext_allocator_malloc_version_1 (i32.const 1))
return
```

this should return to the host the new memoffset where
````
