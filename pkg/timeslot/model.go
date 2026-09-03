package timeslot

import "time"

// TimeSlot represents a guaranteed CPU time slot on a specific core.
// A time slot guarantees `Runtime` nanoseconds of execution every `Period`,
// starting at `Offset` within the period. Slots on the same core are
// non-overlapping: offset_i + runtime_i <= offset_(i+1).
type TimeSlot struct {
	// ID uniquely identifies this slot, formatted as "core<N>-slot<M>".
	ID string

	// Core is the CPU core index this slot is bound to.
	Core int

	// Index is the slot index within the core (0-based).
	Index int

	// Offset is the start time within each period.
	// Slot i starts executing at Offset within each period boundary.
	Offset time.Duration

	// Runtime is the guaranteed execution time per period.
	Runtime time.Duration

	// Period is the scheduling period. All slots on the same core
	// share the same period.
	Period time.Duration

	// NUMANode is the NUMA node ID for this slot's core.
	// Set to -1 if NUMA information is not available.
	NUMANode int

	// CpufreqGovernor is the cpufreq scaling governor (e.g. "performance").
	CpufreqGovernor string

	// CpufreqBaseKhz is the base CPU frequency in KHz. -1 if unavailable.
	CpufreqBaseKhz int64

	// PhysicalPackageID is the CPU socket number. -1 if unavailable.
	PhysicalPackageID int

	// Features is the sorted, comma-separated list of CPU flags present
	// on this core and matching the configured allowlist.
	Features string
}

// Utilization returns the CPU utilization as a fraction (0.0 to 1.0).
func (ts *TimeSlot) Utilization() float64 {
	return float64(ts.Runtime) / float64(ts.Period)
}

// UtilizationMillis returns the CPU utilization in tenths of a percent
// (e.g., 250 means 25.0%). Useful for integer-based ResourceSlice attributes.
func (ts *TimeSlot) UtilizationMillis() int64 {
	return int64(ts.Runtime * 1000 / ts.Period)
}

// CorePartition defines how a single CPU core is divided into time slots.
type CorePartition struct {
	// Core is the CPU core index.
	Core int

	// Period is the scheduling period for all slots on this core.
	Period time.Duration

	// Slots is the ordered list of time slots.
	Slots []TimeSlot
}

// NodeConfig defines the time slot partitioning configuration for a node.
type NodeConfig struct {
	// Cores lists the CPU core indices to partition into time slots.
	Cores []int

	// Period is the scheduling period applied to all cores.
	Period time.Duration

	// SlotCount is the number of equal time slots per core.
	SlotCount int

	// FeatureAllowlist is the set of /proc/cpuinfo flags to publish as
	// device attributes. Nil uses DefaultFeatureAllowlist; an empty
	// slice disables feature discovery.
	FeatureAllowlist []string
}
