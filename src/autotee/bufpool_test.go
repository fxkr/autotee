package autotee

import (
	"testing"
)

func TestBufPoolReferenceCounting(t *testing.T) {
	bp := NewBufPool(16, 64)

	// At least 16 buffers must be immediately available.
	elems := make([]*BufPoolElem, 16)
	for i := 0; i < 16; i++ {
		elems[i] = <-bp.C
		elems[i].AcquireFirst()
	}

	// We can increase/decrease the reference counter as long as it stays >0.
	elems[0].Acquire(1) // => 2
	elems[0].Acquire(2) // => 4
	elems[0].Free()     // => 3
	elems[0].Free()     // => 2
	elems[0].Free()     // => 1

	// After freeing one buffer, we should be able to get a new one immediately.
	elems[0].Free() // => 0
	elems[0] = <-bp.C
	elems[0].AcquireFirst()

	// If all buffers are taken, we can wait until one becomes free.
	go func(e *BufPoolElem) {
		e.Free()
	}(elems[0])
	elems[0] = <-bp.C
	elems[0].AcquireFirst()

	// If we free all 16 buffers, we can get 16 new ones.
	for _, elem := range elems {
		elem.Free()
	}
	for i, _ := range elems {
		elems[i] = <-bp.C
		elems[i].AcquireFirst()
	}
}

func TestBufPoolSizeManagement(t *testing.T) {
	bp := NewBufPool(16, 64)
	elems := make([]*BufPoolElem, 16)
	for i := 0; i < 16; i++ {
		elems[i] = <-bp.C
		elems[i].AcquireFirst()
	}

	// All buffers should have the right size.
	for _, elem := range elems {
		buf := elem.GetBuffer()
		if len(buf) != 64 {
			t.Fatalf("Buffer size was %d, expected 64", len(buf))
		}
	}

	// We should be able to change the buffer size.
	elem := elems[0]
	elem.SetSize(23)
	actualNewLen := len(elem.GetBuffer())
	if actualNewLen != 23 {
		t.Fatalf("New buffer size was %d, expected 64", actualNewLen)
	}

	// We should be able to set it to the maximum again.
	elem.SetSize(64)
	actualNewLen = len(elem.GetBuffer())
	if actualNewLen != 64 {
		t.Fatalf("New buffer size was %d, expected 64", actualNewLen)
	}

	// But setting a too high limit causes panic.
	tooHighLimitTestOk := make(chan bool)
	go func(ok chan bool) {
		defer func() {
			ok <- recover() != nil
		}()
		elem.SetSize(65)
	}(tooHighLimitTestOk)
	if !<-tooHighLimitTestOk {
		t.Fatal("Recover() returned nil")
	}

	// Recycled buffers have the original size again.
	elem.Free()
	elem = <-bp.C
	elem.AcquireFirst()
	buf := elem.GetBuffer()
	if len(buf) != 64 {
		t.Fatalf("Buffer size was %d, expected 64", len(buf))
	}
}

func TestBufPoolIsFree(t *testing.T) {

	bp := NewBufPool(16, 64)

	// At least 16 buffers must be immediately available.
	elems := make([]*BufPoolElem, 16)
	for i := 0; i < 16; i++ {
		elems[i] = <-bp.C
		elems[i].AcquireFirst()
		if bp.IsFull() != false {
			t.Fatal("IsFull() should have returned false")
		}
	}

	for _, elem := range elems {
		if bp.IsFull() != false {
			t.Fatal("IsFull() should have returned false")
		}
		elem.Free()
	}

	if bp.IsFull() != true {
		t.Fatal("IsFull() should have returned true")
	}
}
