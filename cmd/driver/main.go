package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"

	"github.com/fabiendupont/k8s-dra-driver-time-share/pkg/deadline"
	"github.com/fabiendupont/k8s-dra-driver-time-share/pkg/driver"
	"github.com/fabiendupont/k8s-dra-driver-time-share/pkg/timeslot"
)

const driverName = "time-share.fabiendupont.io"

func main() {
	// CDI hook mode: invoked by CRI-O as an OCI hook to apply SCHED_DEADLINE
	// on the container PID. Must run before flag.Parse().
	if len(os.Args) > 1 && os.Args[1] == "--cdi-hook" {
		if err := deadline.RunCDIHook(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "cdi-hook: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	var (
		socketPath  string
		registryDir string
		nodeName    string
		cores       string
		periodMs    int
		slotCount   int
		healthPort  int
	)

	flag.StringVar(&socketPath, "socket", "/var/lib/kubelet/plugins/time-share.fabiendupont.io/plugin.sock", "DRA plugin gRPC socket path")
	flag.StringVar(&registryDir, "registry-dir", "/var/lib/kubelet/plugins_registry", "Kubelet plugin registry directory")
	flag.StringVar(&nodeName, "node-name", "", "Kubernetes node name")
	flag.StringVar(&cores, "cores", "", "Comma-separated list of CPU core indices to partition (e.g., '0,2,4')")
	flag.IntVar(&periodMs, "period-ms", 1, "Scheduling period in milliseconds")
	flag.IntVar(&slotCount, "slot-count", 4, "Number of time slots per core")
	flag.IntVar(&healthPort, "health-port", 8080, "Port for health check endpoints (/healthz, /readyz)")

	klog.InitFlags(nil)
	flag.Parse()

	if nodeName == "" {
		nodeName = os.Getenv("NODE_NAME")
	}
	if nodeName == "" {
		klog.Fatal("--node-name or NODE_NAME required")
	}

	coreList, err := parseCores(cores)
	if err != nil {
		klog.Fatalf("Invalid --cores: %v", err)
	}

	cfg := &timeslot.NodeConfig{
		DriverName: driverName,
		Cores:      coreList,
		Period:     time.Duration(periodMs) * time.Millisecond,
		SlotCount:  slotCount,
	}

	partitions, err := timeslot.PartitionNode(cfg)
	if err != nil {
		klog.Fatalf("Failed to partition cores: %v", err)
	}

	allSlots := timeslot.AllSlots(partitions)
	klog.InfoS("Partitioned cores into time slots",
		"cores", len(coreList),
		"slotsPerCore", slotCount,
		"totalSlots", len(allSlots),
		"periodMs", periodMs,
	)

	state := driver.NewAllocationState(partitions)

	// Install the hook binary to the host-accessible plugin directory and
	// write CDI specs so CRI-O can apply SCHED_DEADLINE at container start.
	pluginDir := filepath.Dir(socketPath)
	hookBinaryPath, err := driver.InstallHookBinaries(pluginDir)
	if err != nil {
		klog.Fatalf("Failed to install hook binary: %v", err)
	}

	cdiDir := "/var/run/cdi"
	if err := driver.WriteCDISpecs(cdiDir, hookBinaryPath, partitions); err != nil {
		klog.Fatalf("Failed to write CDI specs: %v", err)
	}
	defer driver.CleanupCDISpecs(cdiDir)

	driver.RegisterMetrics()
	driver.SlotsTotal.Set(float64(len(allSlots)))

	kubeClient, err := buildKubeClient()
	if err != nil {
		klog.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	publisher := driver.NewSlicePublisher(kubeClient, driverName, nodeName, state)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// Recover allocation state from existing ResourceClaims so the driver
	// can resume after a restart without losing track of active allocations.
	recovered, recoverErr := driver.RecoverAllocations(ctx, kubeClient, driverName, nodeName, state)
	if recoverErr != nil {
		klog.ErrorS(recoverErr, "Partial failure recovering allocations", "recovered", recovered)
	}
	if recovered > 0 {
		driver.SlotsAllocated.Set(float64(recovered))
		klog.InfoS("Recovered slots from previous allocations", "slots", recovered)
	}

	if err := publisher.PublishSlices(ctx, partitions); err != nil {
		klog.Fatalf("Failed to publish ResourceSlices: %v", err)
	}

	drv := driver.NewDriver(driverName, nodeName, kubeClient, state, publisher)

	// Start the health server for liveness/readiness probes.
	healthServer := driver.NewHealthServer(healthPort)
	go func() {
		if err := healthServer.Serve(ctx); err != nil {
			klog.Fatalf("Health server error: %v", err)
		}
	}()

	// Mark ready after ResourceSlice is published and pod watcher is running.
	healthServer.MarkReady()

	// Start the kubelet plugin registration server.
	registrar := driver.NewRegistrar(driverName, socketPath)
	go func() {
		if err := registrar.Serve(ctx, registryDir); err != nil {
			klog.Fatalf("Registration server error: %v", err)
		}
	}()

	if err := runGRPCServer(ctx, socketPath, drv); err != nil {
		klog.Fatalf("gRPC server error: %v", err)
	}

	// Clean up the ResourceSlice so stale devices are not advertised.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cleanupCancel()
	if err := publisher.DeleteSlice(cleanupCtx); err != nil {
		klog.ErrorS(err, "Failed to delete ResourceSlice during shutdown")
	}
}

func runGRPCServer(ctx context.Context, socketPath string, drv *driver.Driver) error {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0750); err != nil {
		return fmt.Errorf("creating socket directory: %w", err)
	}
	_ = os.Remove(socketPath) // clean up stale socket

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", socketPath, err)
	}

	server := grpc.NewServer()
	drav1.RegisterDRAPluginServer(server, drv)

	go func() {
		<-ctx.Done()
		klog.InfoS("Shutting down gRPC server")
		server.GracefulStop()
	}()

	klog.InfoS("gRPC server listening", "socket", socketPath)
	return server.Serve(listener)
}

func parseCores(s string) ([]int, error) {
	if s == "" {
		return nil, fmt.Errorf("no cores specified")
	}

	seen := make(map[int]struct{})
	var cores []int
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			v := s[start:i]
			start = i + 1
			if len(v) == 0 {
				return nil, fmt.Errorf("empty core value at position %d", i)
			}
			n := 0
			for _, c := range v {
				if c < '0' || c > '9' {
					return nil, fmt.Errorf("invalid core value %q", v)
				}
				n = n*10 + int(c-'0')
			}
			if _, dup := seen[n]; dup {
				return nil, fmt.Errorf("duplicate core %d", n)
			}
			seen[n] = struct{}{}
			cores = append(cores, n)
		}
	}
	return cores, nil
}

func buildKubeClient() (kubernetes.Interface, error) {
	var client kubernetes.Interface
	var lastErr error

	for attempt := 0; attempt < 10; attempt++ {
		config, err := rest.InClusterConfig()
		if err != nil {
			lastErr = fmt.Errorf("building in-cluster config: %w", err)
			klog.InfoS("Waiting for in-cluster config", "attempt", attempt+1, "error", err)
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}

		client, err = kubernetes.NewForConfig(config)
		if err != nil {
			lastErr = fmt.Errorf("creating kubernetes client: %w", err)
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}

		// Verify connectivity with a lightweight API call.
		_, err = client.Discovery().ServerVersion()
		if err != nil {
			lastErr = fmt.Errorf("verifying API server connectivity: %w", err)
			klog.InfoS("API server not reachable yet", "attempt", attempt+1, "error", err)
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}

		return client, nil
	}
	return nil, lastErr
}
