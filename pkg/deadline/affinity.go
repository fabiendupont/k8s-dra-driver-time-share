package deadline

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// SetAffinity pins a process to a specific set of CPU cores using
// sched_setaffinity(2). Use pid=0 to target the calling thread.
func SetAffinity(pid int, cores []int) error {
	var set unix.CPUSet
	for _, core := range cores {
		set.Set(core)
	}

	if err := unix.SchedSetaffinity(pid, &set); err != nil {
		return fmt.Errorf("sched_setaffinity(pid=%d, cores=%v): %w", pid, cores, err)
	}
	return nil
}

// GetAffinity returns the CPU affinity mask for a process.
func GetAffinity(pid int) ([]int, error) {
	var set unix.CPUSet

	if err := unix.SchedGetaffinity(pid, &set); err != nil {
		return nil, fmt.Errorf("sched_getaffinity(pid=%d): %w", pid, err)
	}

	var cores []int
	for i := 0; i < 1024; i++ {
		if set.IsSet(i) {
			cores = append(cores, i)
		}
	}
	return cores, nil
}
