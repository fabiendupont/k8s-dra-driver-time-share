package driver

import (
	"context"
	"fmt"

	"k8s.io/klog/v2"
	drav1beta1 "k8s.io/kubelet/pkg/apis/dra/v1beta1"
)

// Driver implements the DRA kubelet plugin DRAPluginServer interface.
type Driver struct {
	driverName string
	nodeName   string
	state      *AllocationState
	publisher  *SlicePublisher
	podWatcher *PodWatcher
}

// NewDriver creates a new DRA driver instance.
func NewDriver(driverName, nodeName string, state *AllocationState, publisher *SlicePublisher, podWatcher *PodWatcher) *Driver {
	return &Driver{
		driverName: driverName,
		nodeName:   nodeName,
		state:      state,
		publisher:  publisher,
		podWatcher: podWatcher,
	}
}

// NodePrepareResources is called by the kubelet when a pod that references
// ResourceClaims is about to start. At this point the container has not
// started yet, so there are no PIDs to configure. The driver records the
// allocation; the PodWatcher will detect the running pod and start cgroup
// watchers to apply SCHED_DEADLINE as processes appear.
func (d *Driver) NodePrepareResources(ctx context.Context, req *drav1beta1.NodePrepareResourcesRequest) (*drav1beta1.NodePrepareResourcesResponse, error) {
	resp := &drav1beta1.NodePrepareResourcesResponse{
		Claims: make(map[string]*drav1beta1.NodePrepareResourceResponse),
	}

	for _, claim := range req.Claims {
		claimResp := d.prepareClaim(ctx, claim)
		resp.Claims[claim.UID] = claimResp
	}

	return resp, nil
}

func (d *Driver) prepareClaim(ctx context.Context, claim *drav1beta1.Claim) *drav1beta1.NodePrepareResourceResponse {
	slots := d.state.SlotsByClaimUID(claim.UID)
	if len(slots) == 0 {
		return &drav1beta1.NodePrepareResourceResponse{
			Error: fmt.Sprintf("no slots allocated for claim %s", claim.UID),
		}
	}

	klog.InfoS("Prepared claim for SCHED_DEADLINE scheduling",
		"claim", claim.UID,
		"namespace", claim.Namespace,
		"name", claim.Name,
		"slots", len(slots),
	)

	// The PodWatcher will detect when the pod is running and start
	// cgroup watchers to apply scheduling to its processes.
	return &drav1beta1.NodePrepareResourceResponse{}
}

// NodeUnprepareResources is called when a pod's ResourceClaims are no longer
// needed. The driver stops cgroup watchers and clears SCHED_DEADLINE from
// any tracked PIDs before releasing the slots.
func (d *Driver) NodeUnprepareResources(ctx context.Context, req *drav1beta1.NodeUnprepareResourcesRequest) (*drav1beta1.NodeUnprepareResourcesResponse, error) {
	resp := &drav1beta1.NodeUnprepareResourcesResponse{
		Claims: make(map[string]*drav1beta1.NodeUnprepareResourceResponse),
	}

	for _, claim := range req.Claims {
		claimResp := d.unprepareClaim(ctx, claim)
		resp.Claims[claim.UID] = claimResp
	}

	return resp, nil
}

func (d *Driver) unprepareClaim(ctx context.Context, claim *drav1beta1.Claim) *drav1beta1.NodeUnprepareResourceResponse {
	// Stop the cgroup watcher first — this clears SCHED_DEADLINE
	// from all tracked PIDs before they lose their guaranteed bandwidth.
	d.podWatcher.stopWatcher(claim.UID)

	released := d.state.ReleaseByClaimUID(claim.UID)
	klog.InfoS("Unprepared claim, released slots",
		"claim", claim.UID, "slots", released)

	return &drav1beta1.NodeUnprepareResourceResponse{}
}
