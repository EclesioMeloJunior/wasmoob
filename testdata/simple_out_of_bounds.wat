(module
   ;; Import a function from the host environment ("env" module)
    ;; that can grow memory. It takes an i32 integer representing the
    ;; number of pages to grow and returns the previous size of memory.
  (func $ext_allocator_malloc_version_1 (import "env" "ext_allocator_malloc_version_1") (param i32) (result i32))

  (memory (export "mem") 1) ;; Allocate 1 page of memory (64KiB)
  (global (export "__heap_base") i32 (i32.const 1024))


  (func (export "out_of_bounds") (result i32) ;; Export a function to cause the error
    (local $memoffset i32)
    (local $counter i32)

    (i32.const 0)
    (local.tee $counter) ;; start counter at 0
    (local.set $memoffset) ;; start memoffset at 0

    (block
      (loop
        
        ;; loop condition check
        (local.get $counter)
        (i32.const 16384) ;; loop until
        (i32.ge_u)
        (br_if 1) ;; break if $counter >= 10

        ;; loop body goes here
        ;; store an i32 into the linear memory
        (i32.store (local.get $memoffset) (i32.const 9090))
        
        ;; increase memoffset by 4 (i32 byte size)
        (local.get $memoffset)
        (i32.const 4)
        (i32.add)
        (local.set $memoffset)

        ;; increases the counter by one
        (local.get $counter)
        (i32.const 1)
        (i32.add)
        (local.set $counter)
        (br 0) ;; start from the begining
      )
    )


    ;;(local $pointer i32)
    ;;(local.set $pointer (i32.const 65536))
    (call $ext_allocator_malloc_version_1 (i32.const 1))
    ;;(local.set $pointer)

    ;;drop
    ;;(i32.store (local.get $pointer) (i32.const 2147483648))
    ;;(i32.load (local.get $pointer))
    ;;(local.get $memoffset)
    return
  )
)
