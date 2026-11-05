package media

import (
	"context"
	"sync"
	"testing"
)

func TestFrameSlotReplacementPreservesReaders(t *testing.T) {
	d, err := Open(context.Background(), Source{URL: fixture(t, false)}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	frame, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	defer frame.Close()
	var slot frameSlot
	slot.store(frame.Clone())
	held := slot.load()
	defer held.Close()
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for range 50 {
				if current := slot.load(); current != nil {
					if _, err := current.RGBA(8, 8); err != nil {
						t.Error("replacing the display frame invalidated a reader", err)
					}
					current.Close()
				}
			}
		})
	}
	for range 50 {
		slot.store(frame.Clone())
	}
	slot.store(nil)
	workers.Wait()
	if remaining := slot.load(); remaining != nil {
		remaining.Close()
		t.Fatal("clearing the display frame retained a reference")
	}
	if _, err := held.RGBA(8, 8); err != nil {
		t.Fatal("clearing the slot invalidated a retained reader", err)
	}
}
