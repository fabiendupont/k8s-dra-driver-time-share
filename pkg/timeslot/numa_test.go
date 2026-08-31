package timeslot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCPUList(t *testing.T) {
	tests := []struct {
		input string
		want  []int
	}{
		{"0", []int{0}},
		{"0-3", []int{0, 1, 2, 3}},
		{"0,2,4", []int{0, 2, 4}},
		{"0-1,4-5", []int{0, 1, 4, 5}},
		{"", nil},
	}

	for _, tt := range tests {
		cpus, err := parseCPUList(tt.input)
		if err != nil {
			t.Errorf("parseCPUList(%q) error: %v", tt.input, err)
			continue
		}
		if len(cpus) != len(tt.want) {
			t.Errorf("parseCPUList(%q) = %v, want %v", tt.input, cpus, tt.want)
			continue
		}
		for i, c := range cpus {
			if c != tt.want[i] {
				t.Errorf("parseCPUList(%q)[%d] = %d, want %d", tt.input, i, c, tt.want[i])
			}
		}
	}
}

func TestLookupNUMA(t *testing.T) {
	tmpDir := t.TempDir()

	// Simulate a 2-NUMA-node system.
	node0 := filepath.Join(tmpDir, "node0")
	node1 := filepath.Join(tmpDir, "node1")
	_ = os.MkdirAll(node0, 0755)
	_ = os.MkdirAll(node1, 0755)
	_ = os.WriteFile(filepath.Join(node0, "cpulist"), []byte("0-3\n"), 0644)
	_ = os.WriteFile(filepath.Join(node1, "cpulist"), []byte("4-7\n"), 0644)

	numaMap, err := LookupNUMA(tmpDir)
	if err != nil {
		t.Fatalf("LookupNUMA failed: %v", err)
	}

	for cpu := 0; cpu <= 3; cpu++ {
		if numaMap[cpu] != 0 {
			t.Errorf("cpu %d: NUMA = %d, want 0", cpu, numaMap[cpu])
		}
	}
	for cpu := 4; cpu <= 7; cpu++ {
		if numaMap[cpu] != 1 {
			t.Errorf("cpu %d: NUMA = %d, want 1", cpu, numaMap[cpu])
		}
	}
}

func TestLookupNUMANotAvailable(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := LookupNUMA(filepath.Join(tmpDir, "nonexistent"))
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}
