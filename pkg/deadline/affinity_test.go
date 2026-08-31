// Tests require appropriate permissions. They call sched_setaffinity on the
// current thread (pid=0). Run with: go test -tags=deadline_test -count=1 ./pkg/deadline/
//
//go:build deadline_test

package deadline

import (
	"testing"
)

func TestSetAndGetAffinity(t *testing.T) {
	// Get the original affinity to restore later.
	original, err := GetAffinity(0)
	if err != nil {
		t.Fatalf("GetAffinity failed: %v", err)
	}
	if len(original) == 0 {
		t.Fatal("original affinity is empty")
	}

	// Pin to the first available core.
	target := []int{original[0]}
	if err := SetAffinity(0, target); err != nil {
		t.Fatalf("SetAffinity failed: %v", err)
	}

	cores, err := GetAffinity(0)
	if err != nil {
		t.Fatalf("GetAffinity after set failed: %v", err)
	}

	if len(cores) != 1 || cores[0] != target[0] {
		t.Errorf("affinity = %v, want %v", cores, target)
	}

	// Restore original affinity.
	if err := SetAffinity(0, original); err != nil {
		t.Fatalf("restoring affinity failed: %v", err)
	}
}

func TestGetAffinityReturnsMultipleCores(t *testing.T) {
	cores, err := GetAffinity(0)
	if err != nil {
		t.Fatalf("GetAffinity failed: %v", err)
	}

	// On most systems the default affinity includes multiple cores.
	// We just check it's non-empty and all values are non-negative.
	if len(cores) == 0 {
		t.Fatal("affinity should include at least one core")
	}
	for _, c := range cores {
		if c < 0 {
			t.Errorf("negative core index: %d", c)
		}
	}
}
