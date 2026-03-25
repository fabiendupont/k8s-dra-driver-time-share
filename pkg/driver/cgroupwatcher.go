package driver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"k8s.io/klog/v2"

	"github.com/fabiendupont/k8s-dra-driver-time-share/pkg/deadline"
	"github.com/fabiendupont/k8s-dra-driver-time-share/pkg/timeslot"
)

const (
	cgroupPollInterval = 100 * time.Millisecond
	cgroupProcsFile    = "cgroup.procs"
)

// CgroupWatcher monitors a cgroup hierarchy for process changes and applies
// SCHED_DEADLINE scheduling + CPU affinity to all PIDs found within it.
// It polls cgroup.procs files because cgroupfs does not support inotify.
type CgroupWatcher struct {
	cgroupPath string
	slot       timeslot.TimeSlot
	claimUID   string

	mu         sync.Mutex
	knownPIDs  map[int]struct{}
	cancel     context.CancelFunc
	stopped    chan struct{}
}

// NewCgroupWatcher creates a watcher for the given cgroup path.
// It does not start watching until Start is called.
func NewCgroupWatcher(cgroupPath string, slot timeslot.TimeSlot, claimUID string) *CgroupWatcher {
	return &CgroupWatcher{
		cgroupPath: cgroupPath,
		slot:       slot,
		claimUID:   claimUID,
		knownPIDs:  make(map[int]struct{}),
		stopped:    make(chan struct{}),
	}
}

// Start begins polling for PID changes. It returns immediately.
// Call Stop to terminate the watcher.
func (w *CgroupWatcher) Start(ctx context.Context) {
	ctx, w.cancel = context.WithCancel(ctx)

	go func() {
		defer close(w.stopped)
		klog.InfoS("Cgroup watcher started",
			"cgroupPath", w.cgroupPath, "slot", w.slot.ID, "claim", w.claimUID)

		ticker := time.NewTicker(cgroupPollInterval)
		defer ticker.Stop()

		// Do an immediate first poll.
		w.poll()

		for {
			select {
			case <-ctx.Done():
				klog.InfoS("Cgroup watcher stopped",
					"cgroupPath", w.cgroupPath, "slot", w.slot.ID)
				return
			case <-ticker.C:
				w.poll()
			}
		}
	}()
}

// Stop terminates the watcher and clears SCHED_DEADLINE from all tracked PIDs.
func (w *CgroupWatcher) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	<-w.stopped
	w.clearAll()
}

// poll reads current PIDs from the cgroup and applies scheduling to new ones.
func (w *CgroupWatcher) poll() {
	currentPIDs, err := w.collectPIDs()
	if err != nil {
		klog.V(4).InfoS("Failed to read cgroup PIDs",
			"cgroupPath", w.cgroupPath, "error", err)
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	currentSet := make(map[int]struct{}, len(currentPIDs))
	for _, pid := range currentPIDs {
		currentSet[pid] = struct{}{}
	}

	// Apply scheduling to new PIDs.
	for _, pid := range currentPIDs {
		if _, known := w.knownPIDs[pid]; known {
			continue
		}
		if err := w.applyScheduling(pid); err != nil {
			SchedDeadlineApplyTotal.WithLabelValues("error").Inc()
			klog.ErrorS(err, "Failed to apply scheduling",
				"pid", pid, "slot", w.slot.ID)
			continue
		}
		SchedDeadlineApplyTotal.WithLabelValues("success").Inc()
		w.knownPIDs[pid] = struct{}{}
		TrackedPIDs.Inc()
	}

	// Remove stale PIDs (process exited).
	for pid := range w.knownPIDs {
		if _, exists := currentSet[pid]; !exists {
			delete(w.knownPIDs, pid)
			TrackedPIDs.Dec()
			klog.V(3).InfoS("Process exited, removed from tracking",
				"pid", pid, "slot", w.slot.ID)
		}
	}
}

// collectPIDs reads PIDs from the cgroup and all its child cgroups.
// Container runtimes often create child cgroups under the pod cgroup.
func (w *CgroupWatcher) collectPIDs() ([]int, error) {
	var allPIDs []int

	err := filepath.WalkDir(w.cgroupPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible directories
		}
		if d.IsDir() {
			procsPath := filepath.Join(path, cgroupProcsFile)
			pids, err := readPIDs(procsPath)
			if err != nil {
				return nil // cgroup.procs may not exist in all directories
			}
			allPIDs = append(allPIDs, pids...)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking cgroup hierarchy at %s: %w", w.cgroupPath, err)
	}

	return allPIDs, nil
}

// applyScheduling sets CPU affinity and SCHED_DEADLINE on a single PID.
func (w *CgroupWatcher) applyScheduling(pid int) error {
	// Pin to the slot's core first.
	if err := deadline.SetAffinity(pid, []int{w.slot.Core}); err != nil {
		return fmt.Errorf("setting CPU affinity: %w", err)
	}

	// Apply SCHED_DEADLINE with the slot's parameters.
	if err := deadline.SetDeadline(
		pid,
		uint64(w.slot.Runtime.Nanoseconds()),
		uint64(w.slot.Period.Nanoseconds()), // deadline = period
		uint64(w.slot.Period.Nanoseconds()),
	); err != nil {
		return fmt.Errorf("setting SCHED_DEADLINE: %w", err)
	}

	klog.InfoS("Applied SCHED_DEADLINE to process",
		"pid", pid,
		"slot", w.slot.ID,
		"core", w.slot.Core,
		"runtimeNs", w.slot.Runtime.Nanoseconds(),
		"periodNs", w.slot.Period.Nanoseconds(),
	)
	return nil
}

// clearAll resets all tracked PIDs back to normal scheduling.
func (w *CgroupWatcher) clearAll() {
	w.mu.Lock()
	defer w.mu.Unlock()

	count := len(w.knownPIDs)
	for pid := range w.knownPIDs {
		if err := deadline.ClearDeadline(pid); err != nil {
			klog.V(3).InfoS("Failed to clear SCHED_DEADLINE (process may have exited)",
				"pid", pid, "error", err)
		}
	}
	w.knownPIDs = make(map[int]struct{})
	TrackedPIDs.Sub(float64(count))
}

// readPIDs reads and parses a cgroup.procs file.
func readPIDs(path string) ([]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)
	if len(content) == 0 {
		return nil, nil
	}

	var pids []int
	start := 0
	for i := 0; i <= len(content); i++ {
		if i == len(content) || content[i] == '\n' {
			if i > start {
				n, err := parseIntFast(content[start:i])
				if err != nil {
					return nil, fmt.Errorf("parsing pid %q: %w", content[start:i], err)
				}
				pids = append(pids, n)
			}
			start = i + 1
		}
	}
	return pids, nil
}

func parseIntFast(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid character %q", c)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
