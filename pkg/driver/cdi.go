package driver

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"k8s.io/klog/v2"

	"github.com/fabiendupont/k8s-dra-driver-time-share/pkg/timeslot"
)

const (
	cdiVendor  = "time-share.fabiendupont.io"
	cdiClass   = "slot"
	cdiVersion = "0.6.0"
)

// CDISpec is a minimal CDI spec structure.
type CDISpec struct {
	CDIVersion string      `json:"cdiVersion"`
	Kind       string      `json:"kind"`
	Devices    []CDIDevice `json:"devices"`
}

// CDIDevice is a device entry in a CDI spec.
type CDIDevice struct {
	Name           string           `json:"name"`
	ContainerEdits CDIContainerEdit `json:"containerEdits"`
}

// CDIContainerEdit defines modifications to apply to a container.
type CDIContainerEdit struct {
	Env   []string  `json:"env,omitempty"`
	Hooks []CDIHook `json:"hooks,omitempty"`
}

// CDIHook is an OCI lifecycle hook.
type CDIHook struct {
	HookName string   `json:"hookName"`
	Path     string   `json:"path"`
	Args     []string `json:"args,omitempty"`
}

// CDIDeviceID returns the CDI device ID for a slot (e.g., "time-share.fabiendupont.io/slot=core0-slot0").
func CDIDeviceID(slotID string) string {
	return cdiVendor + "/" + cdiClass + "=" + slotID
}

// WriteCDISpecs writes CDI spec files for all time slots to the given directory.
// The hook binary path is the location of the driver binary on the host filesystem.
func WriteCDISpecs(cdiDir, hookBinaryPath string, partitions []*timeslot.CorePartition) error {
	if err := os.MkdirAll(cdiDir, 0755); err != nil {
		return fmt.Errorf("creating CDI directory: %w", err)
	}

	var devices []CDIDevice
	for _, p := range partitions {
		for _, slot := range p.Slots {
			dev := CDIDevice{
				Name: slot.ID,
				ContainerEdits: CDIContainerEdit{
					Env: []string{
						fmt.Sprintf("DRA_TIME_SHARE_SLOT=%s", slot.ID),
						fmt.Sprintf("DRA_TIME_SHARE_CORE=%d", slot.Core),
						fmt.Sprintf("DRA_TIME_SHARE_RUNTIME_NS=%d", slot.Runtime.Nanoseconds()),
						fmt.Sprintf("DRA_TIME_SHARE_PERIOD_NS=%d", slot.Period.Nanoseconds()),
					},
					Hooks: []CDIHook{
						{
							HookName: "createRuntime",
							Path:     hookBinaryPath,
							Args: []string{
								hookBinaryPath,
								"--cdi-hook",
								"--core=" + strconv.Itoa(slot.Core),
								"--runtime-ns=" + strconv.FormatInt(slot.Runtime.Nanoseconds(), 10),
								"--period-ns=" + strconv.FormatInt(slot.Period.Nanoseconds(), 10),
							},
						},
					},
				},
			}
			devices = append(devices, dev)
		}
	}

	spec := CDISpec{
		CDIVersion: cdiVersion,
		Kind:       cdiVendor + "/" + cdiClass,
		Devices:    devices,
	}

	specPath := filepath.Join(cdiDir, cdiVendor+"-"+cdiClass+".json")
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling CDI spec: %w", err)
	}

	if err := os.WriteFile(specPath, data, 0644); err != nil {
		return fmt.Errorf("writing CDI spec %s: %w", specPath, err)
	}

	klog.InfoS("Wrote CDI spec", "path", specPath, "devices", len(devices))
	return nil
}

// CleanupCDISpecs removes CDI spec files written by the driver.
func CleanupCDISpecs(cdiDir string) {
	specPath := filepath.Join(cdiDir, cdiVendor+"-"+cdiClass+".json")
	if err := os.Remove(specPath); err != nil && !os.IsNotExist(err) {
		klog.ErrorS(err, "Failed to remove CDI spec", "path", specPath)
	}
}

// InstallHookBinary copies the driver binary to the host-accessible plugin
// directory so CRI-O can execute it as a CDI hook.
func InstallHookBinary(hostPluginDir string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolving self: %w", err)
	}

	hookPath := filepath.Join(hostPluginDir, "dra-time-share-hook")

	srcFile, err := os.Open(self)
	if err != nil {
		return "", fmt.Errorf("opening self binary: %w", err)
	}
	defer func() { _ = srcFile.Close() }()

	dstFile, err := os.OpenFile(hookPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return "", fmt.Errorf("creating hook binary: %w", err)
	}

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		_ = dstFile.Close()
		return "", fmt.Errorf("writing hook binary: %w", err)
	}
	if err := dstFile.Close(); err != nil {
		return "", fmt.Errorf("finalizing hook binary: %w", err)
	}

	klog.InfoS("Installed hook binary", "path", hookPath)
	return hookPath, nil
}
