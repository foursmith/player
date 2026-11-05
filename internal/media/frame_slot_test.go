package media

import (
	"sync"
	"testing"
)

func TestFrameSlotCloseReleasesLateFramesAndPreservesReaders(t *testing.T) {
	d, err := Open(Source{URL: fixture(t, false)}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	frame, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	defer frame.Close()
	var slot FrameSlot
	slot.Store(frame.Clone())
	held := slot.Frame()
	defer held.Close()
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for range 50 {
				slot.Store(frame.Clone())
			}
		})
	}
	slot.Close()
	workers.Wait()
	if remaining := slot.Frame(); remaining != nil {
		remaining.Close()
		t.Fatal("work finishing after cancellation retained a preview frame")
	}
	late := frame.Clone()
	slot.Store(late)
	if late.ptr != nil {
		t.Fatal("closed slot did not release the submitted frame")
	}
	if _, err := held.RGBA(8, 8); err != nil {
		t.Fatal("closing the slot invalidated a retained reader", err)
	}
}
