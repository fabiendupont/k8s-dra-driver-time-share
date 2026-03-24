package timeslot

import (
	"fmt"
	"time"
)

// PartitionCore divides a CPU core into equal, non-overlapping time slots.
// Each slot gets runtime = period / slotCount, with offsets staggered
// so that slot i starts at i * runtime.
func PartitionCore(core int, period time.Duration, slotCount int) (*CorePartition, error) {
	if slotCount <= 0 {
		return nil, fmt.Errorf("slotCount must be positive, got %d", slotCount)
	}
	if period <= 0 {
		return nil, fmt.Errorf("period must be positive, got %s", period)
	}

	runtime := period / time.Duration(slotCount)
	if runtime == 0 {
		return nil, fmt.Errorf("period %s too small for %d slots", period, slotCount)
	}

	slots := make([]TimeSlot, slotCount)
	for i := range slotCount {
		slots[i] = TimeSlot{
			ID:      fmt.Sprintf("core%d-slot%d", core, i),
			Core:    core,
			Index:   i,
			Offset:  time.Duration(i) * runtime,
			Runtime: runtime,
			Period:  period,
		}
	}

	return &CorePartition{
		Core:   core,
		Period: period,
		Slots:  slots,
	}, nil
}

// PartitionNode creates time slot partitions for all configured cores.
func PartitionNode(cfg *NodeConfig) ([]*CorePartition, error) {
	if len(cfg.Cores) == 0 {
		return nil, fmt.Errorf("no cores configured")
	}

	partitions := make([]*CorePartition, 0, len(cfg.Cores))
	for _, core := range cfg.Cores {
		p, err := PartitionCore(core, cfg.Period, cfg.SlotCount)
		if err != nil {
			return nil, fmt.Errorf("partitioning core %d: %w", core, err)
		}
		partitions = append(partitions, p)
	}

	return partitions, nil
}

// AllSlots returns a flat list of all time slots across all partitions.
func AllSlots(partitions []*CorePartition) []TimeSlot {
	var slots []TimeSlot
	for _, p := range partitions {
		slots = append(slots, p.Slots...)
	}
	return slots
}
