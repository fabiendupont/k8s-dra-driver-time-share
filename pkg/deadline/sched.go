package deadline

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	schedDeadline = 6 // SCHED_DEADLINE

	// sched_setattr / sched_getattr syscall numbers (amd64)
	sysSchedSetattr = 314
	sysSchedGetattr = 315
)

// SchedAttr mirrors the Linux sched_attr structure.
// See sched(7) and the kernel's include/uapi/linux/sched/types.h.
type SchedAttr struct {
	Size     uint32
	Policy   uint32
	Flags    uint64
	Nice     int32
	Priority uint32
	Runtime  uint64 // nanoseconds
	Deadline uint64 // nanoseconds
	Period   uint64 // nanoseconds
}

// SetDeadline configures SCHED_DEADLINE on the given PID.
// runtime, deadline, and period are in nanoseconds.
// Use pid=0 to target the calling thread.
func SetDeadline(pid int, runtime, deadline, period uint64) error {
	attr := SchedAttr{
		Size:     uint32(unsafe.Sizeof(SchedAttr{})),
		Policy:   schedDeadline,
		Runtime:  runtime,
		Deadline: deadline,
		Period:   period,
	}

	_, _, errno := unix.Syscall(
		uintptr(sysSchedSetattr),
		uintptr(pid),
		uintptr(unsafe.Pointer(&attr)),
		0, // flags
	)
	if errno != 0 {
		return fmt.Errorf("sched_setattr(pid=%d): %w", pid, errno)
	}
	return nil
}

// ClearDeadline resets the given PID back to SCHED_OTHER (normal scheduling).
func ClearDeadline(pid int) error {
	attr := SchedAttr{
		Size:   uint32(unsafe.Sizeof(SchedAttr{})),
		Policy: 0, // SCHED_OTHER
	}

	_, _, errno := unix.Syscall(
		uintptr(sysSchedSetattr),
		uintptr(pid),
		uintptr(unsafe.Pointer(&attr)),
		0,
	)
	if errno != 0 {
		return fmt.Errorf("sched_setattr(pid=%d, SCHED_OTHER): %w", pid, errno)
	}
	return nil
}

// GetAttr retrieves the scheduling attributes of the given PID.
func GetAttr(pid int) (*SchedAttr, error) {
	attr := SchedAttr{
		Size: uint32(unsafe.Sizeof(SchedAttr{})),
	}

	_, _, errno := unix.Syscall(
		uintptr(sysSchedGetattr),
		uintptr(pid),
		uintptr(unsafe.Pointer(&attr)),
		uintptr(attr.Size),
	)
	if errno != 0 {
		return nil, fmt.Errorf("sched_getattr(pid=%d): %w", pid, errno)
	}
	return &attr, nil
}
