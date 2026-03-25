package driver

import (
	"os"
	"path/filepath"
	"testing"

	v1 "k8s.io/api/core/v1"
)

func TestResolveCgroupPathSystemd(t *testing.T) {
	tmpDir := t.TempDir()
	podUID := "abcd-1234-efgh-5678"
	sanitizedUID := "abcd_1234_efgh_5678"

	// Create systemd-style cgroup path.
	systemdPath := filepath.Join(tmpDir,
		"kubepods.slice",
		"kubepods-burstable.slice",
		"kubepods-burstable-pod"+sanitizedUID+".slice",
	)
	_ = os.MkdirAll(systemdPath, 0755)

	pw := &PodWatcher{cgroupRoot: tmpDir}
	resolved := pw.resolveCgroupPath(podUID, v1.PodQOSBurstable)

	if resolved != systemdPath {
		t.Errorf("resolved = %q, want %q", resolved, systemdPath)
	}
}

func TestResolveCgroupPathCgroupfs(t *testing.T) {
	tmpDir := t.TempDir()
	podUID := "abcd-1234-efgh-5678"

	// Create cgroupfs-style cgroup path.
	cgroupfsPath := filepath.Join(tmpDir,
		"kubepods", "guaranteed", "pod"+podUID)
	_ = os.MkdirAll(cgroupfsPath, 0755)

	pw := &PodWatcher{cgroupRoot: tmpDir}
	resolved := pw.resolveCgroupPath(podUID, v1.PodQOSGuaranteed)

	if resolved != cgroupfsPath {
		t.Errorf("resolved = %q, want %q", resolved, cgroupfsPath)
	}
}

func TestResolveCgroupPathKubeletSlice(t *testing.T) {
	tmpDir := t.TempDir()
	podUID := "aaaa-bbbb-cccc-dddd"
	sanitizedUID := "aaaa_bbbb_cccc_dddd"

	kubeletPath := filepath.Join(tmpDir,
		"kubelet.slice",
		"kubelet-kubepods.slice",
		"kubelet-kubepods-besteffort.slice",
		"kubelet-kubepods-besteffort-pod"+sanitizedUID+".slice",
	)
	_ = os.MkdirAll(kubeletPath, 0755)

	pw := &PodWatcher{cgroupRoot: tmpDir}
	resolved := pw.resolveCgroupPath(podUID, v1.PodQOSBestEffort)

	if resolved != kubeletPath {
		t.Errorf("resolved = %q, want %q", resolved, kubeletPath)
	}
}

func TestResolveCgroupPathNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	pw := &PodWatcher{cgroupRoot: tmpDir}
	resolved := pw.resolveCgroupPath("nonexistent-uid", v1.PodQOSBurstable)

	if resolved != "" {
		t.Errorf("expected empty string, got %q", resolved)
	}
}

func TestQosToCgroupDir(t *testing.T) {
	tests := []struct {
		qos  v1.PodQOSClass
		want string
	}{
		{v1.PodQOSGuaranteed, "guaranteed"},
		{v1.PodQOSBurstable, "burstable"},
		{v1.PodQOSBestEffort, "besteffort"},
	}
	for _, tt := range tests {
		got := qosToCgroupDir(tt.qos)
		if got != tt.want {
			t.Errorf("qosToCgroupDir(%v) = %q, want %q", tt.qos, got, tt.want)
		}
	}
}
