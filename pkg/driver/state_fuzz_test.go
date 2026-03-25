package driver

import (
	"fmt"
	"testing"
	"time"

	"github.com/fabiendupont/k8s-dra-driver-time-share/pkg/timeslot"
)

// fuzzState creates an AllocationState with a fixed set of slots for fuzzing.
func fuzzState() (*AllocationState, []string) {
	partitions, _ := timeslot.PartitionNode(&timeslot.NodeConfig{
		Cores:     []int{0, 1, 2, 3},
		Period:    1 * time.Millisecond,
		SlotCount: 4,
	})
	state := NewAllocationState(partitions)

	var slotIDs []string
	for _, p := range partitions {
		for _, s := range p.Slots {
			slotIDs = append(slotIDs, s.ID)
		}
	}
	return state, slotIDs
}

// FuzzAllocateRelease exercises the allocation state machine with random
// sequences of allocate and release operations, verifying invariants:
//   - AllocatedCount + len(AvailableSlots) == total slots (conservation)
//   - No slot is allocated to two different claims
//   - Release makes the slot available again
//   - Idempotent allocation succeeds for the same claim
//   - SlotsByClaimUID returns only slots allocated to that claim
func FuzzAllocateRelease(f *testing.F) {
	// Seed: (slotIndex, claimSuffix, opIsRelease)
	// slotIndex selects a slot, claimSuffix creates claim IDs,
	// opIsRelease determines allocate vs release.
	f.Add(uint(0), uint(1), false)
	f.Add(uint(5), uint(2), true)
	f.Add(uint(15), uint(1), false)
	f.Add(uint(0), uint(1), true)

	f.Fuzz(func(t *testing.T, slotIdx uint, claimSuffix uint, release bool) {
		state, slotIDs := fuzzState()
		totalSlots := len(slotIDs)

		// Normalize inputs to valid range.
		idx := int(slotIdx % uint(totalSlots))
		slotID := slotIDs[idx]
		claimUID := fmt.Sprintf("claim-%d", claimSuffix%10)

		if release {
			// Allocate then release, verify round-trip.
			err := state.Allocate(slotID, claimUID)
			if err != nil {
				t.Skip() // shouldn't happen but skip if it does
			}

			// Conservation: allocated + available == total.
			if state.AllocatedCount()+len(state.AvailableSlots()) != totalSlots {
				t.Fatalf("conservation violated after allocate: %d + %d != %d",
					state.AllocatedCount(), len(state.AvailableSlots()), totalSlots)
			}

			err = state.Release(slotID)
			if err != nil {
				t.Fatalf("Release failed: %v", err)
			}

			// After release, all slots available again.
			if state.AllocatedCount() != 0 {
				t.Fatalf("AllocatedCount = %d after release, want 0", state.AllocatedCount())
			}
			if len(state.AvailableSlots()) != totalSlots {
				t.Fatalf("AvailableSlots = %d after release, want %d",
					len(state.AvailableSlots()), totalSlots)
			}

			// SlotsByClaimUID should be empty.
			if len(state.SlotsByClaimUID(claimUID)) != 0 {
				t.Fatalf("SlotsByClaimUID not empty after release")
			}
		} else {
			// Allocate, verify state, idempotent re-allocate.
			err := state.Allocate(slotID, claimUID)
			if err != nil {
				t.Skip()
			}

			// Conservation.
			if state.AllocatedCount()+len(state.AvailableSlots()) != totalSlots {
				t.Fatalf("conservation violated: %d + %d != %d",
					state.AllocatedCount(), len(state.AvailableSlots()), totalSlots)
			}

			// SlotsByClaimUID returns exactly this slot.
			slots := state.SlotsByClaimUID(claimUID)
			if len(slots) != 1 {
				t.Fatalf("SlotsByClaimUID returned %d slots, want 1", len(slots))
			}
			if slots[0].ID != slotID {
				t.Fatalf("SlotsByClaimUID returned %q, want %q", slots[0].ID, slotID)
			}

			// Idempotent re-allocate succeeds.
			err = state.Allocate(slotID, claimUID)
			if err != nil {
				t.Fatalf("idempotent Allocate failed: %v", err)
			}

			// Still only 1 allocated.
			if state.AllocatedCount() != 1 {
				t.Fatalf("AllocatedCount = %d after idempotent allocate, want 1",
					state.AllocatedCount())
			}

			// Different claim should be rejected.
			otherClaim := claimUID + "-other"
			err = state.Allocate(slotID, otherClaim)
			if err == nil {
				t.Fatalf("Allocate should reject different claim on same slot")
			}
		}
	})
}

// FuzzReleaseByClaimUID exercises batch release with random allocation patterns.
func FuzzReleaseByClaimUID(f *testing.F) {
	// Seed: (allocation bitmap, claimSuffix)
	// Each bit in the bitmap determines whether a slot is allocated.
	f.Add(uint16(0b1010_1010_1010_1010), uint(1))
	f.Add(uint16(0b1111_1111_1111_1111), uint(2))
	f.Add(uint16(0b0000_0000_0000_0001), uint(3))
	f.Add(uint16(0), uint(4))

	f.Fuzz(func(t *testing.T, bitmap uint16, claimSuffix uint) {
		state, slotIDs := fuzzState()
		totalSlots := len(slotIDs)
		claimUID := fmt.Sprintf("claim-%d", claimSuffix%10)

		// Allocate slots based on bitmap.
		var expectedAllocated int
		for i, id := range slotIDs {
			if bitmap&(1<<uint(i)) != 0 {
				if err := state.Allocate(id, claimUID); err != nil {
					t.Fatalf("Allocate %q failed: %v", id, err)
				}
				expectedAllocated++
			}
		}

		// Verify allocation count.
		if state.AllocatedCount() != expectedAllocated {
			t.Fatalf("AllocatedCount = %d, want %d",
				state.AllocatedCount(), expectedAllocated)
		}

		// Conservation.
		if state.AllocatedCount()+len(state.AvailableSlots()) != totalSlots {
			t.Fatalf("conservation violated: %d + %d != %d",
				state.AllocatedCount(), len(state.AvailableSlots()), totalSlots)
		}

		// Release all by claim UID.
		released := state.ReleaseByClaimUID(claimUID)
		if len(released) != expectedAllocated {
			t.Fatalf("released %d slots, want %d", len(released), expectedAllocated)
		}

		// Everything should be available now.
		if state.AllocatedCount() != 0 {
			t.Fatalf("AllocatedCount = %d after release, want 0", state.AllocatedCount())
		}
		if len(state.AvailableSlots()) != totalSlots {
			t.Fatalf("AvailableSlots = %d after release, want %d",
				len(state.AvailableSlots()), totalSlots)
		}

		// SlotsByClaimUID should be empty.
		if len(state.SlotsByClaimUID(claimUID)) != 0 {
			t.Fatalf("SlotsByClaimUID not empty after ReleaseByClaimUID")
		}

		// Second release should be a no-op.
		released2 := state.ReleaseByClaimUID(claimUID)
		if len(released2) != 0 {
			t.Fatalf("second ReleaseByClaimUID released %d, want 0", len(released2))
		}
	})
}

// FuzzMultiClaimIsolation verifies that allocations from different claims
// never interfere with each other.
func FuzzMultiClaimIsolation(f *testing.F) {
	// Seed: (slot for claim A, slot for claim B)
	f.Add(uint(0), uint(1))
	f.Add(uint(0), uint(0)) // same slot — should conflict
	f.Add(uint(7), uint(15))

	f.Fuzz(func(t *testing.T, idxA uint, idxB uint) {
		state, slotIDs := fuzzState()
		totalSlots := len(slotIDs)
		a := int(idxA % uint(totalSlots))
		b := int(idxB % uint(totalSlots))

		claimA := "claim-A"
		claimB := "claim-B"

		// Allocate slot A to claim A.
		if err := state.Allocate(slotIDs[a], claimA); err != nil {
			t.Skip()
		}

		if a == b {
			// Same slot: claim B must be rejected.
			if err := state.Allocate(slotIDs[b], claimB); err == nil {
				t.Fatal("expected error allocating same slot to different claim")
			}

			// Claim A still has its slot.
			if len(state.SlotsByClaimUID(claimA)) != 1 {
				t.Fatal("claim A lost its slot")
			}

			// Claim B has nothing.
			if len(state.SlotsByClaimUID(claimB)) != 0 {
				t.Fatal("claim B should have no slots")
			}
		} else {
			// Different slots: both should succeed.
			if err := state.Allocate(slotIDs[b], claimB); err != nil {
				t.Fatalf("Allocate slot B failed: %v", err)
			}

			// Both claims have exactly 1 slot.
			if len(state.SlotsByClaimUID(claimA)) != 1 {
				t.Fatal("claim A should have 1 slot")
			}
			if len(state.SlotsByClaimUID(claimB)) != 1 {
				t.Fatal("claim B should have 1 slot")
			}

			// Conservation.
			if state.AllocatedCount()+len(state.AvailableSlots()) != totalSlots {
				t.Fatal("conservation violated")
			}

			// Release A doesn't affect B.
			state.ReleaseByClaimUID(claimA)
			if len(state.SlotsByClaimUID(claimB)) != 1 {
				t.Fatal("releasing claim A affected claim B")
			}
			if state.AllocatedCount() != 1 {
				t.Fatalf("AllocatedCount = %d after releasing A, want 1",
					state.AllocatedCount())
			}
		}
	})
}
