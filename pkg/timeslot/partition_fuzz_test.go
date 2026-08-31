package timeslot

import (
	"fmt"
	"testing"
	"time"
)

// FuzzPartitionCore verifies partitioning invariants for arbitrary inputs:
//   - All slots are non-overlapping
//   - Total runtime does not exceed the period
//   - Each slot has the correct core and period
//   - Slot IDs are unique
//   - Utilization per slot is in (0.0, 1.0]
func FuzzPartitionCore(f *testing.F) {
	// Seed corpus: representative cases including edge cases.
	f.Add(0, int64(1_000_000), 4)   // 1ms, 4 slots
	f.Add(3, int64(500_000), 2)     // 500µs, 2 slots
	f.Add(0, int64(1_000_000), 1)   // 1ms, 1 slot (full core)
	f.Add(7, int64(10_000_000), 10) // 10ms, 10 slots
	f.Add(0, int64(1_000_000), 999) // 1ms, 999 slots (tiny runtime)
	f.Add(0, int64(100_000), 100)   // 100µs, 100 slots (1µs each)

	f.Fuzz(func(t *testing.T, core int, periodNs int64, slotCount int) {
		// Skip invalid inputs that PartitionCore is expected to reject.
		if slotCount <= 0 || periodNs <= 0 || core < 0 {
			t.Skip()
		}

		period := time.Duration(periodNs)
		if period/time.Duration(slotCount) == 0 {
			t.Skip() // runtime would be zero
		}

		p, err := PartitionCore(core, period, slotCount, 0)
		if err != nil {
			// PartitionCore legitimately rejects some inputs.
			t.Skip()
		}

		// Property 1: correct number of slots.
		if len(p.Slots) != slotCount {
			t.Fatalf("got %d slots, want %d", len(p.Slots), slotCount)
		}

		// Property 2: all slots non-overlapping.
		for i := 1; i < len(p.Slots); i++ {
			prevEnd := p.Slots[i-1].Offset + p.Slots[i-1].Runtime
			currStart := p.Slots[i].Offset
			if prevEnd > currStart {
				t.Fatalf("slot %d (ends %s) overlaps slot %d (starts %s)",
					i-1, prevEnd, i, currStart)
			}
		}

		// Property 3: last slot does not exceed the period.
		last := p.Slots[len(p.Slots)-1]
		if last.Offset+last.Runtime > period {
			t.Fatalf("last slot exceeds period: %s + %s > %s",
				last.Offset, last.Runtime, period)
		}

		// Property 4: total runtime does not exceed the period.
		var totalRuntime time.Duration
		for _, s := range p.Slots {
			totalRuntime += s.Runtime
		}
		if totalRuntime > period {
			t.Fatalf("total runtime %s exceeds period %s", totalRuntime, period)
		}

		// Property 5: each slot has correct core, period, and positive runtime.
		for i, s := range p.Slots {
			if s.Core != core {
				t.Fatalf("slot %d: core = %d, want %d", i, s.Core, core)
			}
			if s.Period != period {
				t.Fatalf("slot %d: period = %s, want %s", i, s.Period, period)
			}
			if s.Runtime <= 0 {
				t.Fatalf("slot %d: runtime = %s, must be positive", i, s.Runtime)
			}
			if s.Index != i {
				t.Fatalf("slot %d: index = %d, want %d", i, s.Index, i)
			}
		}

		// Property 6: all slot IDs are unique.
		ids := make(map[string]struct{}, len(p.Slots))
		for _, s := range p.Slots {
			if _, dup := ids[s.ID]; dup {
				t.Fatalf("duplicate slot ID %q", s.ID)
			}
			ids[s.ID] = struct{}{}
		}

		// Property 7: utilization per slot is in (0.0, 1.0].
		for i, s := range p.Slots {
			u := s.Utilization()
			if u <= 0 || u > 1.0 {
				t.Fatalf("slot %d: utilization %f out of range (0, 1]", i, u)
			}
		}

		// Property 8: all slots have equal runtime (equal partitioning).
		expectedRuntime := p.Slots[0].Runtime
		for i, s := range p.Slots {
			if s.Runtime != expectedRuntime {
				t.Fatalf("slot %d: runtime %s != slot 0 runtime %s",
					i, s.Runtime, expectedRuntime)
			}
		}

		// Property 9: slot ID format is "core<N>-slot<M>".
		for i, s := range p.Slots {
			expected := fmt.Sprintf("core%d-slot%d", core, i)
			if s.ID != expected {
				t.Fatalf("slot %d: ID = %q, want %q", i, s.ID, expected)
			}
		}
	})
}
