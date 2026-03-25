package driver

import (
	"testing"
	"time"

	"github.com/fabiendupont/k8s-dra-driver-time-share/pkg/timeslot"
)

func testPartitions() []*timeslot.CorePartition {
	partitions, _ := timeslot.PartitionNode(&timeslot.NodeConfig{
		Cores:     []int{0, 1},
		Period:    1 * time.Millisecond,
		SlotCount: 2,
	})
	return partitions
}

func TestNewAllocationState(t *testing.T) {
	state := NewAllocationState(testPartitions())

	available := state.AvailableSlots()
	if len(available) != 4 {
		t.Fatalf("got %d available slots, want 4", len(available))
	}
}

func TestAllocateAndRelease(t *testing.T) {
	state := NewAllocationState(testPartitions())

	if err := state.Allocate("core0-slot0", "claim-1"); err != nil {
		t.Fatalf("Allocate failed: %v", err)
	}

	available := state.AvailableSlots()
	if len(available) != 3 {
		t.Fatalf("got %d available slots, want 3", len(available))
	}

	if err := state.Release("core0-slot0"); err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	available = state.AvailableSlots()
	if len(available) != 4 {
		t.Fatalf("got %d available slots, want 4", len(available))
	}
}

func TestAllocateUnknownSlot(t *testing.T) {
	state := NewAllocationState(testPartitions())

	if err := state.Allocate("core99-slot0", "claim-1"); err == nil {
		t.Fatal("expected error for unknown slot")
	}
}

func TestAllocateAlreadyAllocated(t *testing.T) {
	state := NewAllocationState(testPartitions())

	if err := state.Allocate("core0-slot0", "claim-1"); err != nil {
		t.Fatalf("first Allocate failed: %v", err)
	}

	if err := state.Allocate("core0-slot0", "claim-2"); err == nil {
		t.Fatal("expected error for double allocation by different claim")
	}
}

func TestAllocateIdempotent(t *testing.T) {
	state := NewAllocationState(testPartitions())

	if err := state.Allocate("core0-slot0", "claim-1"); err != nil {
		t.Fatalf("first Allocate failed: %v", err)
	}

	if err := state.Allocate("core0-slot0", "claim-1"); err != nil {
		t.Fatalf("idempotent Allocate failed: %v", err)
	}

	// Should still be only 3 available (not double-counted).
	if len(state.AvailableSlots()) != 3 {
		t.Fatalf("got %d available, want 3", len(state.AvailableSlots()))
	}
}

func TestReleaseUnknownSlot(t *testing.T) {
	state := NewAllocationState(testPartitions())

	if err := state.Release("core99-slot0"); err == nil {
		t.Fatal("expected error for unknown slot")
	}
}

func TestSlotsByClaimUID(t *testing.T) {
	state := NewAllocationState(testPartitions())

	_ = state.Allocate("core0-slot0", "claim-1")
	_ = state.Allocate("core1-slot0", "claim-1")
	_ = state.Allocate("core0-slot1", "claim-2")

	slots := state.SlotsByClaimUID("claim-1")
	if len(slots) != 2 {
		t.Fatalf("got %d slots for claim-1, want 2", len(slots))
	}

	slots = state.SlotsByClaimUID("claim-2")
	if len(slots) != 1 {
		t.Fatalf("got %d slots for claim-2, want 1", len(slots))
	}

	slots = state.SlotsByClaimUID("claim-none")
	if len(slots) != 0 {
		t.Fatalf("got %d slots for unknown claim, want 0", len(slots))
	}
}

func TestReleaseByClaimUID(t *testing.T) {
	state := NewAllocationState(testPartitions())

	_ = state.Allocate("core0-slot0", "claim-1")
	_ = state.Allocate("core1-slot0", "claim-1")
	_ = state.Allocate("core0-slot1", "claim-2")

	released := state.ReleaseByClaimUID("claim-1")
	if len(released) != 2 {
		t.Fatalf("released %d slots, want 2", len(released))
	}

	if len(state.AvailableSlots()) != 3 {
		t.Fatalf("got %d available slots, want 3", len(state.AvailableSlots()))
	}

	// claim-2 should still be allocated.
	slots := state.SlotsByClaimUID("claim-2")
	if len(slots) != 1 {
		t.Fatalf("claim-2 should still have 1 slot, got %d", len(slots))
	}
}
