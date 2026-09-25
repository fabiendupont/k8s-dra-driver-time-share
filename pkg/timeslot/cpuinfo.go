package timeslot

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const defaultCPUPath = "/sys/devices/system/cpu"

// DefaultFeatureAllowlist is the default set of CPU flags published as device
// attributes. Override with NodeConfig.FeatureAllowlist. An empty allowlist
// disables feature discovery. Entries must match /proc/cpuinfo flag names.
// DefaultFeatureAllowlist contains only features that are rare enough to be
// meaningful for workload placement and relevant to SCHED_DEADLINE use cases
// (RT ML inference, signal processing, crypto offload). Common baseline
// features present on virtually every modern CPU (avx, avx2, fma, sse4, aes
// on most x86; neon on all arm64) are omitted — they don't discriminate.
//
// x86: AMX (Sapphire Rapids+), AVX-512 variants for DSP/ML, AES-NI for NFV
// arm64: SVE/SVE2 (server ARM), BF16/I8MM matrix ops, AES hardware
//
// The combined worst-case length per architecture stays within the 64-byte
// DRA string attribute limit (x86: 59 bytes, arm64: 22 bytes).
var DefaultFeatureAllowlist = []string{
	// x86
	"aes",
	"amx_bf16",
	"amx_int8",
	"amx_tile",
	"avx512f",
	"avx512vl",
	"avx512_vnni",
	// arm64
	"bf16",
	"i8mm",
	"sve",
	"sve2",
}

// CoreInfo holds per-core attributes discovered from sysfs and /proc/cpuinfo.
type CoreInfo struct {
	CpufreqGovernor   string
	CpufreqBaseKhz    int64
	PhysicalPackageID int
	L3CacheID         int
	Features          []string
}

// FeaturesString returns the core's discovered features as a
// comma-separated string suitable for a DRA DeviceAttribute.
func (ci *CoreInfo) FeaturesString() string {
	return strings.Join(ci.Features, ",")
}

// CPUInfoMap maps CPU core index to CoreInfo.
type CPUInfoMap map[int]*CoreInfo

// LookupCPUInfo reads sysfs and /proc/cpuinfo to build per-core attribute
// maps. Only features present in allowlist are retained; pass nil to use
// DefaultFeatureAllowlist. sysfsCPUPath and procCPUInfoPath override the
// default system paths when non-empty (for testing).
func LookupCPUInfo(sysfsCPUPath, procCPUInfoPath string, allowlist []string) (CPUInfoMap, error) {
	if sysfsCPUPath == "" {
		sysfsCPUPath = defaultCPUPath
	}
	if procCPUInfoPath == "" {
		procCPUInfoPath = "/proc/cpuinfo"
	}
	if allowlist == nil {
		allowlist = DefaultFeatureAllowlist
	}

	allowed := make(map[string]bool, len(allowlist))
	for _, f := range allowlist {
		allowed[f] = true
	}

	infoMap := make(CPUInfoMap)

	if err := readSysfsCPUInfo(sysfsCPUPath, infoMap); err != nil {
		return nil, err
	}

	readProcCPUInfo(procCPUInfoPath, infoMap, allowed)

	if len(infoMap) == 0 {
		return nil, fmt.Errorf("no CPU information found")
	}

	return infoMap, nil
}

func readSysfsCPUInfo(sysfsCPUPath string, infoMap CPUInfoMap) error {
	entries, err := os.ReadDir(sysfsCPUPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", sysfsCPUPath, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "cpu") {
			continue
		}

		cpuIDStr := strings.TrimPrefix(entry.Name(), "cpu")
		cpuID, err := strconv.Atoi(cpuIDStr)
		if err != nil {
			continue
		}

		info := &CoreInfo{PhysicalPackageID: -1, CpufreqBaseKhz: -1, L3CacheID: -1}
		cpuDir := filepath.Join(sysfsCPUPath, entry.Name())

		info.CpufreqGovernor = readFileString(filepath.Join(cpuDir, "cpufreq", "scaling_governor"))
		info.CpufreqBaseKhz = readFileInt64(filepath.Join(cpuDir, "cpufreq", "base_frequency"))
		if info.CpufreqBaseKhz < 0 {
			info.CpufreqBaseKhz = readFileInt64(filepath.Join(cpuDir, "cpufreq", "cpuinfo_min_freq"))
		}
		info.PhysicalPackageID = int(readFileInt64(filepath.Join(cpuDir, "topology", "physical_package_id")))
		info.L3CacheID = int(readFileInt64(filepath.Join(cpuDir, "cache", "index3", "id")))

		infoMap[cpuID] = info
	}

	return nil
}

func readProcCPUInfo(procPath string, infoMap CPUInfoMap, allowed map[string]bool) {
	f, err := os.Open(procPath)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	cpuID := -1
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "processor") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				if id, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
					cpuID = id
				}
			}
			continue
		}
		if cpuID < 0 {
			continue
		}
		if strings.HasPrefix(line, "flags") || strings.HasPrefix(line, "Features") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				continue
			}
			features := filterFeatures(strings.TrimSpace(parts[1]), allowed)

			if info, ok := infoMap[cpuID]; ok {
				info.Features = features
			} else {
				infoMap[cpuID] = &CoreInfo{
					PhysicalPackageID: -1,
					CpufreqBaseKhz:    -1,
					Features:          features,
				}
			}
			cpuID = -1
		}
	}
}

func filterFeatures(flagsLine string, allowed map[string]bool) []string {
	if len(allowed) == 0 {
		return nil
	}
	var features []string
	for _, flag := range strings.Fields(flagsLine) {
		if allowed[flag] {
			features = append(features, flag)
		}
	}
	sort.Strings(features)
	return features
}

func readFileString(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readFileInt64(path string) int64 {
	s := readFileString(path)
	if s == "" {
		return -1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return -1
	}
	return v
}
