package driver

import (
	"context"
	"testing"
	"time"

	resourceapi "k8s.io/api/resource/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	drav1beta1 "k8s.io/kubelet/pkg/apis/dra/v1beta1"

	"github.com/fabiendupont/k8s-dra-driver-time-share/pkg/timeslot"
)

const testDriverName = "test-driver.example.com"

func newTestDriver(t *testing.T, claims ...*resourceapi.ResourceClaim) *Driver {
	t.Helper()

	partitions, err := timeslot.PartitionNode(&timeslot.NodeConfig{
		Cores:     []int{0},
		Period:    1 * time.Millisecond,
		SlotCount: 2,
	})
	if err != nil {
		t.Fatalf("partition: %v", err)
	}

	state := NewAllocationState(partitions)
	client := fake.NewSimpleClientset()

	for _, claim := range claims {
		_, err := client.ResourceV1beta1().ResourceClaims(claim.Namespace).Create(
			context.Background(), claim, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("creating test claim: %v", err)
		}
	}

	return NewDriver(testDriverName, "test-node", client, state, nil, nil)
}

func TestNodePrepareResources(t *testing.T) {
	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-claim",
			Namespace: "default",
			UID:       "uid-123",
		},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{
							Request: "slot",
							Driver:  testDriverName,
							Pool:    "test-node",
							Device:  "core0-slot0",
						},
					},
				},
			},
		},
	}

	drv := newTestDriver(t, claim)

	resp, err := drv.NodePrepareResources(context.Background(), &drav1beta1.NodePrepareResourcesRequest{
		Claims: []*drav1beta1.Claim{
			{Namespace: "default", UID: "uid-123", Name: "test-claim"},
		},
	})
	if err != nil {
		t.Fatalf("NodePrepareResources failed: %v", err)
	}

	claimResp := resp.Claims["uid-123"]
	if claimResp.Error != "" {
		t.Fatalf("unexpected error: %s", claimResp.Error)
	}

	slots := drv.state.SlotsByClaimUID("uid-123")
	if len(slots) != 1 {
		t.Fatalf("got %d allocated slots, want 1", len(slots))
	}
	if slots[0].ID != "core0-slot0" {
		t.Errorf("slot ID = %q, want %q", slots[0].ID, "core0-slot0")
	}
}

func TestNodePrepareResourcesClaimNotFound(t *testing.T) {
	drv := newTestDriver(t)

	resp, err := drv.NodePrepareResources(context.Background(), &drav1beta1.NodePrepareResourcesRequest{
		Claims: []*drav1beta1.Claim{
			{Namespace: "default", UID: "uid-missing", Name: "no-such-claim"},
		},
	})
	if err != nil {
		t.Fatalf("NodePrepareResources failed: %v", err)
	}

	claimResp := resp.Claims["uid-missing"]
	if claimResp.Error == "" {
		t.Fatal("expected error for missing claim")
	}
}

func TestNodePrepareResourcesNoAllocation(t *testing.T) {
	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unallocated",
			Namespace: "default",
			UID:       "uid-456",
		},
	}

	drv := newTestDriver(t, claim)

	resp, err := drv.NodePrepareResources(context.Background(), &drav1beta1.NodePrepareResourcesRequest{
		Claims: []*drav1beta1.Claim{
			{Namespace: "default", UID: "uid-456", Name: "unallocated"},
		},
	})
	if err != nil {
		t.Fatalf("NodePrepareResources failed: %v", err)
	}

	claimResp := resp.Claims["uid-456"]
	if claimResp.Error == "" {
		t.Fatal("expected error for unallocated claim")
	}
}

func TestNodePrepareResourcesWrongDriver(t *testing.T) {
	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-driver-claim",
			Namespace: "default",
			UID:       "uid-789",
		},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{
							Request: "slot",
							Driver:  "other-driver.example.com",
							Pool:    "test-node",
							Device:  "core0-slot0",
						},
					},
				},
			},
		},
	}

	drv := newTestDriver(t, claim)

	resp, err := drv.NodePrepareResources(context.Background(), &drav1beta1.NodePrepareResourcesRequest{
		Claims: []*drav1beta1.Claim{
			{Namespace: "default", UID: "uid-789", Name: "other-driver-claim"},
		},
	})
	if err != nil {
		t.Fatalf("NodePrepareResources failed: %v", err)
	}

	claimResp := resp.Claims["uid-789"]
	if claimResp.Error == "" {
		t.Fatal("expected error when no devices match our driver")
	}
}

func TestNodeUnprepareResources(t *testing.T) {
	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-claim",
			Namespace: "default",
			UID:       "uid-123",
		},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{
							Request: "slot",
							Driver:  testDriverName,
							Pool:    "test-node",
							Device:  "core0-slot0",
						},
					},
				},
			},
		},
	}

	drv := newTestDriver(t, claim)

	// Prepare first.
	_, _ = drv.NodePrepareResources(context.Background(), &drav1beta1.NodePrepareResourcesRequest{
		Claims: []*drav1beta1.Claim{
			{Namespace: "default", UID: "uid-123", Name: "test-claim"},
		},
	})

	// Unprepare — need a PodWatcher for stopWatcher calls.
	partitions, _ := timeslot.PartitionNode(&timeslot.NodeConfig{
		Cores:     []int{0},
		Period:    1 * time.Millisecond,
		SlotCount: 2,
	})
	drv.podWatcher = &PodWatcher{
		state:    drv.state,
		watchers: make(map[string]*CgroupWatcher),
	}
	_ = partitions

	resp, err := drv.NodeUnprepareResources(context.Background(), &drav1beta1.NodeUnprepareResourcesRequest{
		Claims: []*drav1beta1.Claim{
			{Namespace: "default", UID: "uid-123", Name: "test-claim"},
		},
	})
	if err != nil {
		t.Fatalf("NodeUnprepareResources failed: %v", err)
	}

	claimResp := resp.Claims["uid-123"]
	if claimResp.Error != "" {
		t.Fatalf("unexpected error: %s", claimResp.Error)
	}

	slots := drv.state.SlotsByClaimUID("uid-123")
	if len(slots) != 0 {
		t.Fatalf("got %d slots after unprepare, want 0", len(slots))
	}

	if len(drv.state.AvailableSlots()) != 2 {
		t.Fatalf("got %d available slots, want 2", len(drv.state.AvailableSlots()))
	}
}
