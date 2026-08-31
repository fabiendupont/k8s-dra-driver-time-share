// Tests require CAP_SYS_NICE or root. They call sched_setattr on the current
// thread (pid=0). Run with: go test -tags=deadline_test -count=1 ./pkg/deadline/
//
//go:build deadline_test

package deadline

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestSetAndGetDeadline(t *testing.T) {
	var runtimeNs uint64 = 100_000  // 100µs
	var periodNs uint64 = 1_000_000 // 1ms

	if err := SetDeadline(0, runtimeNs, periodNs, periodNs); err != nil {
		t.Fatalf("SetDeadline failed: %v", err)
	}

	attr, err := GetAttr(0)
	if err != nil {
		t.Fatalf("GetAttr failed: %v", err)
	}

	if attr.Policy != unix.SCHED_DEADLINE {
		t.Errorf("policy = %d, want SCHED_DEADLINE (%d)", attr.Policy, unix.SCHED_DEADLINE)
	}
	if attr.Runtime != runtimeNs {
		t.Errorf("runtime = %d, want %d", attr.Runtime, runtimeNs)
	}
	if attr.Period != periodNs {
		t.Errorf("period = %d, want %d", attr.Period, periodNs)
	}
	if attr.Deadline != periodNs {
		t.Errorf("deadline = %d, want %d", attr.Deadline, periodNs)
	}
}

func TestClearDeadline(t *testing.T) {
	// Set SCHED_DEADLINE first.
	if err := SetDeadline(0, 100_000, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("SetDeadline failed: %v", err)
	}

	if err := ClearDeadline(0); err != nil {
		t.Fatalf("ClearDeadline failed: %v", err)
	}

	attr, err := GetAttr(0)
	if err != nil {
		t.Fatalf("GetAttr failed: %v", err)
	}

	if attr.Policy != unix.SCHED_NORMAL {
		t.Errorf("policy = %d after clear, want SCHED_NORMAL (%d)", attr.Policy, unix.SCHED_NORMAL)
	}
}

func TestGetAttrDefaultPolicy(t *testing.T) {
	// Ensure we're on SCHED_NORMAL before checking.
	_ = ClearDeadline(0)

	attr, err := GetAttr(0)
	if err != nil {
		t.Fatalf("GetAttr failed: %v", err)
	}

	if attr.Policy != unix.SCHED_NORMAL {
		t.Errorf("default policy = %d, want SCHED_NORMAL (%d)", attr.Policy, unix.SCHED_NORMAL)
	}
}
