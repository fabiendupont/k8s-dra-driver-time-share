package timeslot

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const defaultNodePath = "/sys/devices/system/node"

// NUMAMap maps CPU core index to NUMA node ID.
type NUMAMap map[int]int

// LookupNUMA reads /sys/devices/system/node/nodeN/cpulist to build a mapping
// of CPU core indices to NUMA node IDs. Returns an error if sysfs is not
// accessible. The caller uses -1 as the NUMA node ID when lookup fails.
func LookupNUMA(sysfsNodePath string) (NUMAMap, error) {
	if sysfsNodePath == "" {
		sysfsNodePath = defaultNodePath
	}

	entries, err := os.ReadDir(sysfsNodePath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", sysfsNodePath, err)
	}

	numaMap := make(NUMAMap)

	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "node") {
			continue
		}

		nodeIDStr := strings.TrimPrefix(entry.Name(), "node")
		nodeID, err := strconv.Atoi(nodeIDStr)
		if err != nil {
			continue
		}

		cpulistPath := filepath.Join(sysfsNodePath, entry.Name(), "cpulist")
		data, err := os.ReadFile(cpulistPath)
		if err != nil {
			continue
		}

		cpus, err := parseCPUList(strings.TrimSpace(string(data)))
		if err != nil {
			continue
		}

		for _, cpu := range cpus {
			numaMap[cpu] = nodeID
		}
	}

	if len(numaMap) == 0 {
		return nil, fmt.Errorf("no NUMA node information found in %s", sysfsNodePath)
	}

	return numaMap, nil
}

// parseCPUList parses a Linux CPU list string like "0-3,8-11" into individual CPU indices.
func parseCPUList(s string) ([]int, error) {
	if s == "" {
		return nil, nil
	}

	var cpus []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if dashIdx := strings.Index(part, "-"); dashIdx >= 0 {
			start, err := strconv.Atoi(part[:dashIdx])
			if err != nil {
				return nil, fmt.Errorf("parsing range start %q: %w", part, err)
			}
			end, err := strconv.Atoi(part[dashIdx+1:])
			if err != nil {
				return nil, fmt.Errorf("parsing range end %q: %w", part, err)
			}
			for i := start; i <= end; i++ {
				cpus = append(cpus, i)
			}
		} else {
			cpu, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("parsing cpu %q: %w", part, err)
			}
			cpus = append(cpus, cpu)
		}
	}
	return cpus, nil
}
