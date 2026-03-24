package driver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fabiendupont/k8s-dra-driver-deterministic-time-share/pkg/timeslot"
)

func TestReadPIDs(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []int
		wantErr bool
	}{
		{
			name:    "single PID",
			content: "1234\n",
			want:    []int{1234},
		},
		{
			name:    "multiple PIDs",
			content: "100\n200\n300\n",
			want:    []int{100, 200, 300},
		},
		{
			name:    "empty file",
			content: "",
			want:    nil,
		},
		{
			name:    "trailing newline",
			content: "42\n",
			want:    []int{42},
		},
		{
			name:    "invalid content",
			content: "abc\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			path := filepath.Join(tmpDir, "cgroup.procs")
			if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}

			pids, err := readPIDs(path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(pids) != len(tt.want) {
				t.Fatalf("got %d PIDs, want %d", len(pids), len(tt.want))
			}
			for i, pid := range pids {
				if pid != tt.want[i] {
					t.Errorf("pid[%d] = %d, want %d", i, pid, tt.want[i])
				}
			}
		})
	}
}

func TestCollectPIDs(t *testing.T) {
	// Simulate a cgroup hierarchy with nested child cgroups.
	tmpDir := t.TempDir()

	// Parent cgroup with PIDs.
	writeProcs(t, tmpDir, "10\n20\n")

	// Child cgroup (like a container scope).
	childDir := filepath.Join(tmpDir, "container1.scope")
	os.MkdirAll(childDir, 0755)
	writeProcs(t, childDir, "30\n40\n")

	// Another child.
	child2Dir := filepath.Join(tmpDir, "container2.scope")
	os.MkdirAll(child2Dir, 0755)
	writeProcs(t, child2Dir, "50\n")

	slot := timeslot.TimeSlot{
		ID:      "core0-slot0",
		Core:    0,
		Runtime: 250 * time.Microsecond,
		Period:  1 * time.Millisecond,
	}

	w := NewCgroupWatcher(tmpDir, slot, "test-claim")
	pids, err := w.collectPIDs()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pids) != 5 {
		t.Fatalf("got %d PIDs, want 5: %v", len(pids), pids)
	}
}

func TestCgroupWatcherDetectsNewPIDs(t *testing.T) {
	tmpDir := t.TempDir()
	writeProcs(t, tmpDir, "")

	slot := timeslot.TimeSlot{
		ID:      "core0-slot0",
		Core:    0,
		Runtime: 250 * time.Microsecond,
		Period:  1 * time.Millisecond,
	}

	w := NewCgroupWatcher(tmpDir, slot, "test-claim")

	// Simulate a poll with no PIDs.
	w.poll()
	if len(w.knownPIDs) != 0 {
		t.Fatalf("expected 0 known PIDs, got %d", len(w.knownPIDs))
	}

	// Write PIDs to simulate container start.
	// Note: applyScheduling will fail since these aren't real PIDs,
	// so we check that the watcher attempts to process them.
	writeProcs(t, tmpDir, "999\n")
	w.poll()
	// The PID won't be in knownPIDs because applyScheduling fails
	// on non-existent PIDs, but the watcher should not crash.
}

func TestCgroupWatcherStartStop(t *testing.T) {
	tmpDir := t.TempDir()
	writeProcs(t, tmpDir, "")

	slot := timeslot.TimeSlot{
		ID:      "core0-slot0",
		Core:    0,
		Runtime: 250 * time.Microsecond,
		Period:  1 * time.Millisecond,
	}

	w := NewCgroupWatcher(tmpDir, slot, "test-claim")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.Start(ctx)

	// Give the watcher time to start.
	time.Sleep(50 * time.Millisecond)

	// Stop should not hang.
	done := make(chan struct{})
	go func() {
		w.Stop()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return within timeout")
	}
}

func TestCgroupWatcherRemovesStalePIDs(t *testing.T) {
	tmpDir := t.TempDir()
	writeProcs(t, tmpDir, "")

	slot := timeslot.TimeSlot{
		ID:      "core0-slot0",
		Core:    0,
		Runtime: 250 * time.Microsecond,
		Period:  1 * time.Millisecond,
	}

	w := NewCgroupWatcher(tmpDir, slot, "test-claim")
	// Manually add a PID as if it was previously tracked.
	w.knownPIDs[42] = struct{}{}

	// Poll with empty cgroup.procs — PID 42 should be removed.
	w.poll()
	if _, exists := w.knownPIDs[42]; exists {
		t.Fatal("stale PID 42 was not removed")
	}
}

func writeProcs(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
