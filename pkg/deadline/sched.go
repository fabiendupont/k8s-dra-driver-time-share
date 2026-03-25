package deadline

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// SetDeadline configures SCHED_DEADLINE on the given PID.
// runtime, deadline, and period are in nanoseconds.
// Use pid=0 to target the calling thread.
func SetDeadline(pid int, runtime, deadline, period uint64) error {
	attr := unix.SchedAttr{
		Policy:   unix.SCHED_DEADLINE,
		Runtime:  runtime,
		Deadline: deadline,
		Period:   period,
	}

	if err := unix.SchedSetAttr(pid, &attr, 0); err != nil {
		return fmt.Errorf("sched_setattr(pid=%d): %w", pid, err)
	}
	return nil
}

// ClearDeadline resets the given PID back to SCHED_OTHER (normal scheduling).
func ClearDeadline(pid int) error {
	attr := unix.SchedAttr{
		Policy: unix.SCHED_NORMAL,
	}

	if err := unix.SchedSetAttr(pid, &attr, 0); err != nil {
		return fmt.Errorf("sched_setattr(pid=%d, SCHED_OTHER): %w", pid, err)
	}
	return nil
}

// GetAttr retrieves the scheduling attributes of the given PID.
func GetAttr(pid int) (*unix.SchedAttr, error) {
	attr, err := unix.SchedGetAttr(pid, 0)
	if err != nil {
		return nil, fmt.Errorf("sched_getattr(pid=%d): %w", pid, err)
	}
	return attr, nil
}
