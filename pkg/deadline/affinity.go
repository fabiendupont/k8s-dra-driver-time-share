package deadline

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// SetAffinity pins a process to a specific set of CPU cores using
// sched_setaffinity(2). Use pid=0 to target the calling thread.
func SetAffinity(pid int, cores []int) error {
	var set unix.CPUSet
	for _, core := range cores {
		set.Set(core)
	}

	_, _, errno := unix.RawSyscall(
		unix.SYS_SCHED_SETAFFINITY,
		uintptr(pid),
		unsafe.Sizeof(set),
		uintptr(unsafe.Pointer(&set)),
	)
	if errno != 0 {
		return fmt.Errorf("sched_setaffinity(pid=%d, cores=%v): %w", pid, cores, errno)
	}
	return nil
}

// GetAffinity returns the CPU affinity mask for a process.
func GetAffinity(pid int) ([]int, error) {
	var set unix.CPUSet

	_, _, errno := unix.RawSyscall(
		unix.SYS_SCHED_GETAFFINITY,
		uintptr(pid),
		unsafe.Sizeof(set),
		uintptr(unsafe.Pointer(&set)),
	)
	if errno != 0 {
		return nil, fmt.Errorf("sched_getaffinity(pid=%d): %w", pid, errno)
	}

	var cores []int
	for i := 0; i < 1024; i++ {
		if set.IsSet(i) {
			cores = append(cores, i)
		}
	}
	return cores, nil
}
