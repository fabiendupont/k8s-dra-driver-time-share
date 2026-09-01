package deadline

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// OCIState is the subset of OCI runtime state passed to hooks on stdin.
type OCIState struct {
	PID int `json:"pid"`
}

// RunCDIHook is the entry point for the CDI/OCI hook mode. It reads OCI state
// from stdin to get the container PID, then delegates to sched-helper — a
// statically linked, single-threaded C binary that handles cgroup migration,
// CPU affinity, and sched_setattr(SCHED_DEADLINE).
//
// Go's multi-threaded runtime prevents calling sched_setattr directly: the
// kernel rejects the syscall when the calling process has threads in non-root
// cgroups.
//
// Args: --cdi-hook --core=N --runtime-ns=R --period-ns=P
func RunCDIHook(args []string) error {
	var core int
	var runtimeNs, periodNs uint64

	for _, arg := range args {
		switch {
		case hasPrefix(arg, "--core="):
			core, _ = strconv.Atoi(arg[len("--core="):])
		case hasPrefix(arg, "--runtime-ns="):
			runtimeNs, _ = strconv.ParseUint(arg[len("--runtime-ns="):], 10, 64)
		case hasPrefix(arg, "--period-ns="):
			periodNs, _ = strconv.ParseUint(arg[len("--period-ns="):], 10, 64)
		}
	}

	if runtimeNs == 0 || periodNs == 0 {
		return fmt.Errorf("--runtime-ns and --period-ns are required")
	}

	state, err := readOCIState(os.Stdin)
	if err != nil {
		return fmt.Errorf("reading OCI state: %w", err)
	}

	if state.PID == 0 {
		return fmt.Errorf("OCI state has no PID")
	}

	self, _ := os.Executable()
	helperPath := filepath.Join(filepath.Dir(self), "sched-helper")

	cmd := exec.Command(helperPath,
		strconv.Itoa(state.PID),
		strconv.Itoa(core),
		strconv.FormatUint(runtimeNs, 10),
		strconv.FormatUint(periodNs, 10),
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(output))
		if strings.Contains(msg, "sched_setattr failed") {
			return fmt.Errorf("SCHED_DEADLINE enforcement failed: %s. "+
				"This kernel likely has CONFIG_RT_GROUP_SCHED=y. "+
				"Use kernel-rt (OpenShift PerformanceProfile) or a kernel "+
				"without CONFIG_RT_GROUP_SCHED.", msg)
		}
		return fmt.Errorf("sched-helper: %s (%w)", msg, err)
	}

	return nil
}

func readOCIState(r io.Reader) (*OCIState, error) {
	var state OCIState
	if err := json.NewDecoder(r).Decode(&state); err != nil {
		return nil, err
	}
	return &state, nil
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
