package driver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/fabiendupont/k8s-dra-driver-time-share/pkg/timeslot"
)

// PodWatcher watches pods on this node for ResourceClaim references,
// discovers their cgroup paths, and starts CgroupWatchers to apply
// SCHED_DEADLINE scheduling.
type PodWatcher struct {
	client     kubernetes.Interface
	nodeName   string
	cgroupRoot string
	state      *AllocationState

	mu            sync.Mutex
	watchers      map[string]*CgroupWatcher // keyed by claimUID
	claimUIDCache map[string]string         // namespace/name -> UID
}

// NewPodWatcher creates a watcher that monitors pods on the given node.
func NewPodWatcher(client kubernetes.Interface, nodeName, cgroupRoot string, state *AllocationState) *PodWatcher {
	return &PodWatcher{
		client:        client,
		nodeName:      nodeName,
		cgroupRoot:    cgroupRoot,
		state:         state,
		watchers:      make(map[string]*CgroupWatcher),
		claimUIDCache: make(map[string]string),
	}
}

// Start begins watching for pods on this node. Blocks until ctx is cancelled.
func (pw *PodWatcher) Start(ctx context.Context) error {
	factory := informers.NewSharedInformerFactoryWithOptions(
		pw.client,
		30*time.Second,
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.FieldSelector = fields.OneTermEqualSelector("spec.nodeName", pw.nodeName).String()
		}),
	)

	podInformer := factory.Core().V1().Pods().Informer()
	_, _ = podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod, ok := obj.(*v1.Pod)
			if !ok {
				return
			}
			pw.handlePod(ctx, pod)
		},
		UpdateFunc: func(_, newObj interface{}) {
			pod, ok := newObj.(*v1.Pod)
			if !ok {
				return
			}
			pw.handlePod(ctx, pod)
		},
		DeleteFunc: func(obj interface{}) {
			pod, ok := obj.(*v1.Pod)
			if !ok {
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					return
				}
				pod, ok = tombstone.Obj.(*v1.Pod)
				if !ok {
					return
				}
			}
			pw.handlePodDelete(pod)
		},
	})

	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	klog.InfoS("Pod watcher started", "node", pw.nodeName)
	<-ctx.Done()

	pw.stopAll()
	return nil
}

// handlePod checks if a pod references any of our allocated claims and
// starts cgroup watchers for any that have a running container.
func (pw *PodWatcher) handlePod(ctx context.Context, pod *v1.Pod) {
	if pod.Status.Phase != v1.PodRunning {
		return
	}

	claimUIDs := pw.podClaimUIDs(ctx, pod)
	if len(claimUIDs) == 0 {
		return
	}

	podUID := string(pod.UID)
	cgroupPath := pw.resolveCgroupPath(podUID, pod.Status.QOSClass)
	if cgroupPath == "" {
		klog.V(4).InfoS("Could not resolve cgroup path for pod",
			"pod", pod.Name, "uid", podUID)
		return
	}

	for _, claimUID := range claimUIDs {
		pw.ensureWatcher(ctx, claimUID, podUID, cgroupPath)
	}
}

// handlePodDelete stops any cgroup watchers associated with the deleted pod.
func (pw *PodWatcher) handlePodDelete(pod *v1.Pod) {
	claimUIDs := pw.podClaimUIDs(context.Background(), pod)
	for _, claimUID := range claimUIDs {
		pw.stopWatcher(claimUID)
	}
}

// podClaimUIDs returns the UIDs of ResourceClaims referenced by the pod
// that are managed by our driver (i.e., have allocated slots in our state).
func (pw *PodWatcher) podClaimUIDs(ctx context.Context, pod *v1.Pod) []string {
	var result []string
	for _, claim := range pod.Spec.ResourceClaims {
		if claim.ResourceClaimName == nil {
			continue
		}
		claimUID := pw.resolveClaimUID(ctx, pod.Namespace, *claim.ResourceClaimName)
		if claimUID == "" {
			continue
		}
		slots := pw.state.SlotsByClaimUID(claimUID)
		if len(slots) > 0 {
			result = append(result, claimUID)
		}
	}
	return result
}

// resolveClaimUID looks up a ResourceClaim by namespace/name and returns its UID.
// Results are cached to avoid repeated API calls on every pod event.
// Cached entries are validated against the allocation state to detect
// recycled claim names (same name, new UID).
func (pw *PodWatcher) resolveClaimUID(ctx context.Context, namespace, name string) string {
	key := namespace + "/" + name

	pw.mu.Lock()
	cachedUID, cached := pw.claimUIDCache[key]
	pw.mu.Unlock()

	// If cached and the UID has active allocations, it's still valid.
	if cached && len(pw.state.SlotsByClaimUID(cachedUID)) > 0 {
		return cachedUID
	}

	claim, err := pw.client.ResourceV1beta1().ResourceClaims(namespace).Get(
		ctx, name, metav1.GetOptions{})
	if err != nil {
		klog.V(4).InfoS("Failed to get ResourceClaim",
			"namespace", namespace, "name", name, "error", err)
		return ""
	}

	uid := string(claim.UID)
	pw.mu.Lock()
	pw.claimUIDCache[key] = uid
	pw.mu.Unlock()

	return uid
}

// ensureWatcher starts a CgroupWatcher for the given claim if one isn't
// already running.
func (pw *PodWatcher) ensureWatcher(ctx context.Context, claimUID, podUID, cgroupPath string) {
	pw.mu.Lock()
	defer pw.mu.Unlock()

	if _, exists := pw.watchers[claimUID]; exists {
		return
	}

	slots := pw.state.SlotsByClaimUID(claimUID)
	if len(slots) == 0 {
		return
	}

	// One claim = one slot (one device in DRA terms).
	slot := slots[0]

	watcher := NewCgroupWatcher(cgroupPath, slot, claimUID)
	watcher.Start(ctx)
	pw.watchers[claimUID] = watcher
	ActiveWatchersGauge.Inc()

	klog.InfoS("Started cgroup watcher for claim",
		"claim", claimUID, "pod", podUID, "cgroupPath", cgroupPath,
		"slot", slot.ID)
}

// StopWatcher stops the cgroup watcher for a given claim.
func (pw *PodWatcher) stopWatcher(claimUID string) {
	pw.mu.Lock()
	watcher, exists := pw.watchers[claimUID]
	if exists {
		delete(pw.watchers, claimUID)
	}
	pw.mu.Unlock()

	if exists {
		watcher.Stop()
		ActiveWatchersGauge.Dec()
		klog.InfoS("Stopped cgroup watcher for claim", "claim", claimUID)
	}
}

// stopAll stops all active cgroup watchers.
func (pw *PodWatcher) stopAll() {
	pw.mu.Lock()
	watchers := make(map[string]*CgroupWatcher, len(pw.watchers))
	for k, v := range pw.watchers {
		watchers[k] = v
	}
	pw.watchers = make(map[string]*CgroupWatcher)
	pw.mu.Unlock()

	for claimUID, watcher := range watchers {
		watcher.Stop()
		ActiveWatchersGauge.Dec()
		klog.InfoS("Stopped cgroup watcher during shutdown", "claim", claimUID)
	}
}

// resolveCgroupPath finds the pod's cgroup directory on the filesystem.
// It supports both systemd and cgroupfs cgroup drivers.
func (pw *PodWatcher) resolveCgroupPath(podUID string, qos v1.PodQOSClass) string {
	// systemd cgroup driver layout:
	// <root>/kubepods.slice/kubepods-<qos>.slice/kubepods-<qos>-pod<uid>.slice/
	//
	// cgroupfs cgroup driver layout:
	// <root>/kubepods/<qos>/pod<uid>/

	qosDir := qosToCgroupDir(qos)
	sanitizedUID := strings.ReplaceAll(podUID, "-", "_")

	// Try systemd layout first (most common with modern distributions).
	systemdPath := filepath.Join(pw.cgroupRoot,
		"kubepods.slice",
		fmt.Sprintf("kubepods-%s.slice", qosDir),
		fmt.Sprintf("kubepods-%s-pod%s.slice", qosDir, sanitizedUID),
	)
	if dirExists(systemdPath) {
		return systemdPath
	}

	// Try cgroupfs layout.
	cgroupfsPath := filepath.Join(pw.cgroupRoot,
		"kubepods", qosDir, "pod"+podUID)
	if dirExists(cgroupfsPath) {
		return cgroupfsPath
	}

	// Try kubelet.slice prefix (some distributions).
	kubeletSlicePath := filepath.Join(pw.cgroupRoot,
		"kubelet.slice",
		"kubelet-kubepods.slice",
		fmt.Sprintf("kubelet-kubepods-%s.slice", qosDir),
		fmt.Sprintf("kubelet-kubepods-%s-pod%s.slice", qosDir, sanitizedUID),
	)
	if dirExists(kubeletSlicePath) {
		return kubeletSlicePath
	}

	return ""
}

func qosToCgroupDir(qos v1.PodQOSClass) string {
	switch qos {
	case v1.PodQOSGuaranteed:
		return "guaranteed"
	case v1.PodQOSBurstable:
		return "burstable"
	case v1.PodQOSBestEffort:
		return "besteffort"
	default:
		return "besteffort"
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ActiveWatchers returns the number of currently running cgroup watchers.
func (pw *PodWatcher) ActiveWatchers() int {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	return len(pw.watchers)
}

// WatchedSlots returns the time slots for which there are active cgroup
// watchers, keyed by claim UID.
func (pw *PodWatcher) WatchedSlots() map[string]timeslot.TimeSlot {
	pw.mu.Lock()
	defer pw.mu.Unlock()

	result := make(map[string]timeslot.TimeSlot, len(pw.watchers))
	for claimUID, watcher := range pw.watchers {
		result[claimUID] = watcher.slot
	}
	return result
}
