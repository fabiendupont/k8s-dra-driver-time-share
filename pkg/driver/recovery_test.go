package driver

import (
	"context"
	"testing"
	"time"

	resourceapi "k8s.io/api/resource/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/fabiendupont/k8s-dra-driver-time-share/pkg/timeslot"
)

func newTestState() *AllocationState {
	partitions, _ := timeslot.PartitionNode(&timeslot.NodeConfig{
		Cores:     []int{0, 1},
		Period:    1 * time.Millisecond,
		SlotCount: 2,
	})
	return NewAllocationState(partitions)
}

func TestRecoverAllocations(t *testing.T) {
	client := fake.NewSimpleClientset()
	state := newTestState()

	// Create a claim allocated to our driver on our node.
	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-claim",
			Namespace: "default",
			UID:       types.UID("uid-existing"),
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
	_, _ = client.ResourceV1beta1().ResourceClaims("default").Create(
		context.Background(), claim, metav1.CreateOptions{})

	recovered, err := RecoverAllocations(context.Background(), client, testDriverName, "test-node", state)
	if err != nil {
		t.Fatalf("RecoverAllocations failed: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered %d slots, want 1", recovered)
	}

	slots := state.SlotsByClaimUID("uid-existing")
	if len(slots) != 1 {
		t.Fatalf("got %d slots for claim, want 1", len(slots))
	}
	if slots[0].ID != "core0-slot0" {
		t.Errorf("slot ID = %q, want %q", slots[0].ID, "core0-slot0")
	}

	if len(state.AvailableSlots()) != 3 {
		t.Fatalf("got %d available, want 3", len(state.AvailableSlots()))
	}
}

func TestRecoverAllocationsSkipsOtherNodes(t *testing.T) {
	client := fake.NewSimpleClientset()
	state := newTestState()

	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-node-claim",
			Namespace: "default",
			UID:       types.UID("uid-other"),
		},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{
							Request: "slot",
							Driver:  testDriverName,
							Pool:    "other-node",
							Device:  "core0-slot0",
						},
					},
				},
			},
		},
	}
	_, _ = client.ResourceV1beta1().ResourceClaims("default").Create(
		context.Background(), claim, metav1.CreateOptions{})

	recovered, err := RecoverAllocations(context.Background(), client, testDriverName, "test-node", state)
	if err != nil {
		t.Fatalf("RecoverAllocations failed: %v", err)
	}
	if recovered != 0 {
		t.Fatalf("recovered %d slots, want 0", recovered)
	}

	if len(state.AvailableSlots()) != 4 {
		t.Fatalf("got %d available, want 4", len(state.AvailableSlots()))
	}
}

func TestRecoverAllocationsSkipsOtherDrivers(t *testing.T) {
	client := fake.NewSimpleClientset()
	state := newTestState()

	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-driver-claim",
			Namespace: "default",
			UID:       types.UID("uid-other-driver"),
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
	_, _ = client.ResourceV1beta1().ResourceClaims("default").Create(
		context.Background(), claim, metav1.CreateOptions{})

	recovered, err := RecoverAllocations(context.Background(), client, testDriverName, "test-node", state)
	if err != nil {
		t.Fatalf("RecoverAllocations failed: %v", err)
	}
	if recovered != 0 {
		t.Fatalf("recovered %d slots, want 0", recovered)
	}
}

func TestRecoverAllocationsNoClaims(t *testing.T) {
	client := fake.NewSimpleClientset()
	state := newTestState()

	recovered, err := RecoverAllocations(context.Background(), client, testDriverName, "test-node", state)
	if err != nil {
		t.Fatalf("RecoverAllocations failed: %v", err)
	}
	if recovered != 0 {
		t.Fatalf("recovered %d slots, want 0", recovered)
	}
}

func TestRecoverAllocationsIdempotent(t *testing.T) {
	client := fake.NewSimpleClientset()
	state := newTestState()

	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "claim",
			Namespace: "default",
			UID:       types.UID("uid-idem"),
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
	_, _ = client.ResourceV1beta1().ResourceClaims("default").Create(
		context.Background(), claim, metav1.CreateOptions{})

	// Recover twice — should be idempotent thanks to Allocate idempotency.
	_, _ = RecoverAllocations(context.Background(), client, testDriverName, "test-node", state)
	recovered, err := RecoverAllocations(context.Background(), client, testDriverName, "test-node", state)
	if err != nil {
		t.Fatalf("second RecoverAllocations failed: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered %d slots on second call, want 1", recovered)
	}

	if len(state.AvailableSlots()) != 3 {
		t.Fatalf("got %d available, want 3", len(state.AvailableSlots()))
	}
}
