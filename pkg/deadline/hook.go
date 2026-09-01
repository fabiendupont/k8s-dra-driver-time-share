package deadline

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
)

// OCIState is the subset of OCI runtime state passed to hooks on stdin.
type OCIState struct {
	PID int `json:"pid"`
}

// RunCDIHook is the entry point for the CDI/OCI hook mode. It reads OCI state
// from stdin to get the container PID, then applies sched_setaffinity and
// sched_setattr(SCHED_DEADLINE) on that PID.
//
// Args: --cdi-hook --core=N --runtime-ns=R --period-ns=P
//
// The hook runs as a host process invoked by CRI-O during container creation.
// Requires a kernel without CONFIG_RT_GROUP_SCHED (kernel-rt, Fedora, upstream).
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

	if err := SetAffinity(state.PID, []int{core}); err != nil {
		return fmt.Errorf("sched_setaffinity(pid=%d, core=%d): %w", state.PID, core, err)
	}

	if err := SetDeadline(state.PID, runtimeNs, periodNs, periodNs); err != nil {
		return fmt.Errorf("sched_setattr(pid=%d): %w. "+
			"If the kernel has CONFIG_RT_GROUP_SCHED=y, switch to kernel-rt "+
			"or a kernel without CONFIG_RT_GROUP_SCHED", state.PID, err)
	}

	fmt.Fprintf(os.Stderr, "Applied SCHED_DEADLINE to container pid %d (core=%d runtime=%dns period=%dns)\n",
		state.PID, core, runtimeNs, periodNs)
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
