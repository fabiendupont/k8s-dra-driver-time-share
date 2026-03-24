package driver

import (
	"fmt"
	"sync"

	"github.com/fabiendupont/k8s-dra-driver-deterministic-time-share/pkg/timeslot"
)

// SlotState tracks whether a time slot is available or allocated.
type SlotState struct {
	Slot      timeslot.TimeSlot
	Allocated bool
	ClaimUID  string
}

// AllocationState manages the availability and allocation of time slots.
type AllocationState struct {
	mu    sync.Mutex
	slots map[string]*SlotState // keyed by slot ID
}

// NewAllocationState creates state from the given partitions, with all slots available.
func NewAllocationState(partitions []*timeslot.CorePartition) *AllocationState {
	slots := make(map[string]*SlotState)
	for _, p := range partitions {
		for _, s := range p.Slots {
			slots[s.ID] = &SlotState{Slot: s}
		}
	}
	return &AllocationState{slots: slots}
}

// AvailableSlots returns all unallocated slots.
func (a *AllocationState) AvailableSlots() []timeslot.TimeSlot {
	a.mu.Lock()
	defer a.mu.Unlock()

	var available []timeslot.TimeSlot
	for _, s := range a.slots {
		if !s.Allocated {
			available = append(available, s.Slot)
		}
	}
	return available
}

// Allocate marks a slot as allocated for a given claim.
func (a *AllocationState) Allocate(slotID, claimUID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	s, ok := a.slots[slotID]
	if !ok {
		return fmt.Errorf("slot %q not found", slotID)
	}
	if s.Allocated {
		return fmt.Errorf("slot %q already allocated to claim %q", slotID, s.ClaimUID)
	}

	s.Allocated = true
	s.ClaimUID = claimUID
	return nil
}

// Release marks a slot as available again.
func (a *AllocationState) Release(slotID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	s, ok := a.slots[slotID]
	if !ok {
		return fmt.Errorf("slot %q not found", slotID)
	}

	s.Allocated = false
	s.ClaimUID = ""
	return nil
}

// ReleaseByClaimUID releases all slots allocated to a given claim.
func (a *AllocationState) ReleaseByClaimUID(claimUID string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	var released []string
	for id, s := range a.slots {
		if s.Allocated && s.ClaimUID == claimUID {
			s.Allocated = false
			s.ClaimUID = ""
			released = append(released, id)
		}
	}
	return released
}

// SlotsByClaimUID returns all slots allocated to a given claim.
func (a *AllocationState) SlotsByClaimUID(claimUID string) []timeslot.TimeSlot {
	a.mu.Lock()
	defer a.mu.Unlock()

	var result []timeslot.TimeSlot
	for _, s := range a.slots {
		if s.Allocated && s.ClaimUID == claimUID {
			result = append(result, s.Slot)
		}
	}
	return result
}
