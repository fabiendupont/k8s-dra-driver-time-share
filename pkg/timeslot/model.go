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
	// DriverName is the DRA driver name registered with Kubernetes.
	DriverName string

	// Cores lists the CPU core indices to partition into time slots.
	Cores []int

	// Period is the scheduling period applied to all cores.
	Period time.Duration

	// SlotCount is the number of equal time slots per core.
	SlotCount int
}

// AllocatedSlot tracks a time slot that has been claimed by a pod.
type AllocatedSlot struct {
	TimeSlot

	// ClaimUID is the Kubernetes ResourceClaim UID.
	ClaimUID string

	// PodUID is the UID of the pod using this claim.
	PodUID string

	// PIDs tracks the process IDs that have been configured with
	// SCHED_DEADLINE for this slot.
	PIDs []int
}
