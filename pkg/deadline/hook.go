package deadline

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// OCIState is the subset of OCI runtime state passed to hooks on stdin.
type OCIState struct {
	PID int `json:"pid"`
}

// RunCDIHook is the entry point for the CDI/OCI hook mode. It reads OCI state
// from stdin to get the container PID, then applies sched_setaffinity and
// sched_setattr(SCHED_DEADLINE) on that PID.
//
// Runs as a poststart OCI hook so it fires after exec() and after CRI-O's
// execCPUAffinity, ensuring the single-core pin wins over any cpuset-wide
// affinity applied earlier by the runtime.
//
// Args: --cdi-hook --core=N --runtime-ns=R --period-ns=P
//
// The hook runs as a host process invoked by CRI-O during container creation.
// Requires a kernel without CONFIG_RT_GROUP_SCHED (kernel-rt, Fedora, upstream).
func RunCDIHook(args []string) error {
	var (
		core      int
		runtimeNs uint64
		periodNs  uint64
		parseErr  error
	)

	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--core="):
			core, parseErr = strconv.Atoi(arg[len("--core="):])
		case strings.HasPrefix(arg, "--runtime-ns="):
			runtimeNs, parseErr = strconv.ParseUint(arg[len("--runtime-ns="):], 10, 64)
		case strings.HasPrefix(arg, "--period-ns="):
			periodNs, parseErr = strconv.ParseUint(arg[len("--period-ns="):], 10, 64)
		}
		if parseErr != nil {
			return fmt.Errorf("invalid argument %q: %w", arg, parseErr)
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
		return fmt.Errorf("kernel may have CONFIG_RT_GROUP_SCHED=y (use kernel-rt): sched_setattr(pid=%d): %w",
			state.PID, err)
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
