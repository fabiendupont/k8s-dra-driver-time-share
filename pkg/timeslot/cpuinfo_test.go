package timeslot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookupCPUInfoSysfs(t *testing.T) {
	tmpDir := t.TempDir()

	// Simulate two CPUs with sysfs entries.
	for _, cpu := range []struct {
		name      string
		governor  string
		baseFreq  string
		packageID string
	}{
		{"cpu0", "performance", "2400000", "0"},
		{"cpu1", "powersave", "1800000", "0"},
	} {
		cpuDir := filepath.Join(tmpDir, cpu.name)
		_ = os.MkdirAll(filepath.Join(cpuDir, "cpufreq"), 0755)
		_ = os.MkdirAll(filepath.Join(cpuDir, "topology"), 0755)
		_ = os.WriteFile(filepath.Join(cpuDir, "cpufreq", "scaling_governor"), []byte(cpu.governor+"\n"), 0644)
		_ = os.WriteFile(filepath.Join(cpuDir, "cpufreq", "base_frequency"), []byte(cpu.baseFreq+"\n"), 0644)
		_ = os.WriteFile(filepath.Join(cpuDir, "topology", "physical_package_id"), []byte(cpu.packageID+"\n"), 0644)
	}

	// Empty /proc/cpuinfo — no features.
	procPath := filepath.Join(tmpDir, "cpuinfo")
	_ = os.WriteFile(procPath, []byte(""), 0644)

	infoMap, err := LookupCPUInfo(tmpDir, procPath, []string{})
	if err != nil {
		t.Fatalf("LookupCPUInfo failed: %v", err)
	}

	if infoMap[0].CpufreqGovernor != "performance" {
		t.Errorf("cpu0 governor = %q, want %q", infoMap[0].CpufreqGovernor, "performance")
	}
	if infoMap[1].CpufreqGovernor != "powersave" {
		t.Errorf("cpu1 governor = %q, want %q", infoMap[1].CpufreqGovernor, "powersave")
	}
	if infoMap[0].CpufreqBaseKhz != 2400000 {
		t.Errorf("cpu0 base freq = %d, want 2400000", infoMap[0].CpufreqBaseKhz)
	}
	if infoMap[1].CpufreqBaseKhz != 1800000 {
		t.Errorf("cpu1 base freq = %d, want 1800000", infoMap[1].CpufreqBaseKhz)
	}
	if infoMap[0].PhysicalPackageID != 0 {
		t.Errorf("cpu0 package = %d, want 0", infoMap[0].PhysicalPackageID)
	}
}

func TestLookupCPUInfoFallbackMinFreq(t *testing.T) {
	tmpDir := t.TempDir()

	cpuDir := filepath.Join(tmpDir, "cpu0", "cpufreq")
	_ = os.MkdirAll(cpuDir, 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, "cpu0", "topology"), 0755)
	// No base_frequency, only cpuinfo_min_freq.
	_ = os.WriteFile(filepath.Join(cpuDir, "cpuinfo_min_freq"), []byte("800000\n"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "cpu0", "topology", "physical_package_id"), []byte("0\n"), 0644)

	procPath := filepath.Join(tmpDir, "cpuinfo")
	_ = os.WriteFile(procPath, []byte(""), 0644)

	infoMap, err := LookupCPUInfo(tmpDir, procPath, []string{})
	if err != nil {
		t.Fatalf("LookupCPUInfo failed: %v", err)
	}

	if infoMap[0].CpufreqBaseKhz != 800000 {
		t.Errorf("cpu0 base freq = %d, want 800000 (fallback to cpuinfo_min_freq)", infoMap[0].CpufreqBaseKhz)
	}
}

func TestLookupCPUInfoFeatures(t *testing.T) {
	tmpDir := t.TempDir()

	// Minimal sysfs entry.
	cpuDir := filepath.Join(tmpDir, "cpu0")
	_ = os.MkdirAll(filepath.Join(cpuDir, "cpufreq"), 0755)
	_ = os.MkdirAll(filepath.Join(cpuDir, "topology"), 0755)
	_ = os.WriteFile(filepath.Join(cpuDir, "cpufreq", "scaling_governor"), []byte("performance\n"), 0644)
	_ = os.WriteFile(filepath.Join(cpuDir, "topology", "physical_package_id"), []byte("0\n"), 0644)

	procPath := filepath.Join(tmpDir, "cpuinfo")
	_ = os.WriteFile(procPath, []byte(
		"processor\t: 0\n"+
			"flags\t\t: fpu vme de sse4_1 sse4_2 avx avx2 avx512f aes rdrand bogoflag\n",
	), 0644)

	infoMap, err := LookupCPUInfo(tmpDir, procPath, []string{"avx2", "avx512f", "aes", "sse4_2"})
	if err != nil {
		t.Fatalf("LookupCPUInfo failed: %v", err)
	}

	info := infoMap[0]
	got := info.FeaturesString()
	want := "aes,avx2,avx512f,sse4_2"
	if got != want {
		t.Errorf("features = %q, want %q", got, want)
	}
}

func TestLookupCPUInfoHybridFeatures(t *testing.T) {
	tmpDir := t.TempDir()

	// P-core with AVX-512, E-core without.
	for _, cpu := range []struct {
		name  string
		flags string
	}{
		{"cpu0", "processor\t: 0\nflags\t\t: avx avx2 avx512f fma\n"},
		{"cpu1", "processor\t: 1\nflags\t\t: avx avx2 fma\n"},
	} {
		cpuDir := filepath.Join(tmpDir, cpu.name)
		_ = os.MkdirAll(filepath.Join(cpuDir, "cpufreq"), 0755)
		_ = os.MkdirAll(filepath.Join(cpuDir, "topology"), 0755)
		_ = os.WriteFile(filepath.Join(cpuDir, "cpufreq", "scaling_governor"), []byte("performance\n"), 0644)
		_ = os.WriteFile(filepath.Join(cpuDir, "topology", "physical_package_id"), []byte("0\n"), 0644)
	}

	procPath := filepath.Join(tmpDir, "cpuinfo")
	_ = os.WriteFile(procPath, []byte(
		"processor\t: 0\nflags\t\t: avx avx2 avx512f fma\n\n"+
			"processor\t: 1\nflags\t\t: avx avx2 fma\n",
	), 0644)

	infoMap, err := LookupCPUInfo(tmpDir, procPath, nil)
	if err != nil {
		t.Fatalf("LookupCPUInfo failed: %v", err)
	}

	got0 := infoMap[0].FeaturesString()
	got1 := infoMap[1].FeaturesString()

	if got0 == got1 {
		t.Errorf("P-core and E-core have identical features %q; expected different sets", got0)
	}

	// P-core should have avx512f, E-core should not.
	if !containsFeature(infoMap[0].Features, "avx512f") {
		t.Errorf("cpu0 (P-core) missing avx512f, got %q", got0)
	}
	if containsFeature(infoMap[1].Features, "avx512f") {
		t.Errorf("cpu1 (E-core) should not have avx512f, got %q", got1)
	}
}

func TestLookupCPUInfoDefaultAllowlist(t *testing.T) {
	tmpDir := t.TempDir()

	cpuDir := filepath.Join(tmpDir, "cpu0")
	_ = os.MkdirAll(filepath.Join(cpuDir, "cpufreq"), 0755)
	_ = os.MkdirAll(filepath.Join(cpuDir, "topology"), 0755)
	_ = os.WriteFile(filepath.Join(cpuDir, "cpufreq", "scaling_governor"), []byte("performance\n"), 0644)
	_ = os.WriteFile(filepath.Join(cpuDir, "topology", "physical_package_id"), []byte("0\n"), 0644)

	procPath := filepath.Join(tmpDir, "cpuinfo")
	_ = os.WriteFile(procPath, []byte(
		"processor\t: 0\n"+
			"flags\t\t: fpu vme avx2 bogus_flag_123 aes\n",
	), 0644)

	// nil allowlist = use default.
	infoMap, err := LookupCPUInfo(tmpDir, procPath, nil)
	if err != nil {
		t.Fatalf("LookupCPUInfo failed: %v", err)
	}

	if containsFeature(infoMap[0].Features, "bogus_flag_123") {
		t.Errorf("bogus flag should be filtered out, got %v", infoMap[0].Features)
	}
	if !containsFeature(infoMap[0].Features, "avx2") {
		t.Errorf("avx2 should be present in default allowlist, got %v", infoMap[0].Features)
	}
}

func TestLookupCPUInfoNotAvailable(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := LookupCPUInfo(filepath.Join(tmpDir, "nonexistent"), "", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent sysfs path")
	}
}

func containsFeature(features []string, f string) bool {
	for _, feat := range features {
		if feat == f {
			return true
		}
	}
	return false
}
