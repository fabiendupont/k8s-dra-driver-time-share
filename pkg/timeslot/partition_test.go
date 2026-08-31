package timeslot

import (
	"testing"
	"time"
)

func TestPartitionCore(t *testing.T) {
	tests := []struct {
		name      string
		core      int
		period    time.Duration
		slotCount int
		wantErr   bool
		wantSlots int
	}{
		{
			name:      "4 slots on 1ms period",
			core:      0,
			period:    1 * time.Millisecond,
			slotCount: 4,
			wantSlots: 4,
		},
		{
			name:      "10 slots on 1ms period",
			core:      2,
			period:    1 * time.Millisecond,
			slotCount: 10,
			wantSlots: 10,
		},
		{
			name:      "single slot",
			core:      1,
			period:    500 * time.Microsecond,
			slotCount: 1,
			wantSlots: 1,
		},
		{
			name:      "zero slot count",
			core:      0,
			period:    1 * time.Millisecond,
			slotCount: 0,
			wantErr:   true,
		},
		{
			name:      "zero period",
			core:      0,
			period:    0,
			slotCount: 4,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := PartitionCore(tt.core, tt.period, tt.slotCount, 0)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(p.Slots) != tt.wantSlots {
				t.Fatalf("got %d slots, want %d", len(p.Slots), tt.wantSlots)
			}

			expectedRuntime := tt.period / time.Duration(tt.slotCount)

			for i, slot := range p.Slots {
				if slot.Core != tt.core {
					t.Errorf("slot %d: core = %d, want %d", i, slot.Core, tt.core)
				}
				if slot.Index != i {
					t.Errorf("slot %d: index = %d, want %d", i, slot.Index, i)
				}
				if slot.Runtime != expectedRuntime {
					t.Errorf("slot %d: runtime = %s, want %s", i, slot.Runtime, expectedRuntime)
				}
				if slot.Period != tt.period {
					t.Errorf("slot %d: period = %s, want %s", i, slot.Period, tt.period)
				}

				expectedOffset := time.Duration(i) * expectedRuntime
				if slot.Offset != expectedOffset {
					t.Errorf("slot %d: offset = %s, want %s", i, slot.Offset, expectedOffset)
				}
			}
		})
	}
}

func TestPartitionCoreSlotsNonOverlapping(t *testing.T) {
	p, err := PartitionCore(0, 1*time.Millisecond, 4, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i := 1; i < len(p.Slots); i++ {
		prevEnd := p.Slots[i-1].Offset + p.Slots[i-1].Runtime
		currStart := p.Slots[i].Offset
		if prevEnd > currStart {
			t.Errorf("slot %d (ends %s) overlaps with slot %d (starts %s)",
				i-1, prevEnd, i, currStart)
		}
	}

	lastSlot := p.Slots[len(p.Slots)-1]
	if lastSlot.Offset+lastSlot.Runtime > p.Period {
		t.Errorf("last slot exceeds period: %s + %s > %s",
			lastSlot.Offset, lastSlot.Runtime, p.Period)
	}
}

func TestPartitionNode(t *testing.T) {
	cfg := &NodeConfig{
		Cores:     []int{0, 2, 4},
		Period:    1 * time.Millisecond,
		SlotCount: 4,
	}

	partitions, err := PartitionNode(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(partitions) != 3 {
		t.Fatalf("got %d partitions, want 3", len(partitions))
	}

	allSlots := AllSlots(partitions)
	if len(allSlots) != 12 {
		t.Fatalf("got %d total slots, want 12", len(allSlots))
	}
}

func TestTimeSlotUtilization(t *testing.T) {
	slot := TimeSlot{
		Runtime: 250 * time.Microsecond,
		Period:  1 * time.Millisecond,
	}

	util := slot.Utilization()
	if util != 0.25 {
		t.Errorf("utilization = %f, want 0.25", util)
	}

	millis := slot.UtilizationMillis()
	if millis != 250 {
		t.Errorf("utilization millis = %d, want 250", millis)
	}
}
