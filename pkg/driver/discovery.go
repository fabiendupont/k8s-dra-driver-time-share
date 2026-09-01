package driver

import (
	"context"
	"fmt"

	resourceapi "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/fabiendupont/k8s-dra-driver-time-share/pkg/timeslot"
)

// SlicePublisher publishes and updates ResourceSlices for time slot devices.
type SlicePublisher struct {
	client     kubernetes.Interface
	driverName string
	nodeName   string
}

// NewSlicePublisher creates a publisher that manages ResourceSlices.
func NewSlicePublisher(client kubernetes.Interface, driverName, nodeName string) *SlicePublisher {
	return &SlicePublisher{
		client:     client,
		driverName: driverName,
		nodeName:   nodeName,
	}
}

// PublishSlices creates or updates the ResourceSlice for available time slots.
func (sp *SlicePublisher) PublishSlices(ctx context.Context, partitions []*timeslot.CorePartition) error {
	devices := sp.buildDevices(partitions)

	ownerRef, err := sp.nodeOwnerReference(ctx)
	if err != nil {
		klog.ErrorS(err, "Could not set Node owner reference on ResourceSlice; slice will not be garbage-collected on node removal")
	}

	slice := &resourceapi.ResourceSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("%s-%s", sp.nodeName, sp.driverName),
			OwnerReferences: ownerRef,
		},
		Spec: resourceapi.ResourceSliceSpec{
			Driver:   sp.driverName,
			NodeName: &sp.nodeName,
			Pool: resourceapi.ResourcePool{
				Name:               sp.nodeName,
				ResourceSliceCount: 1,
			},
			Devices: devices,
		},
	}

	_, err = sp.client.ResourceV1().ResourceSlices().Create(ctx, slice, metav1.CreateOptions{})
	if err == nil {
		klog.InfoS("Created ResourceSlice", "name", slice.Name, "devices", len(devices))
		return nil
	}

	// Slice already exists — fetch and update.
	existing, getErr := sp.client.ResourceV1().ResourceSlices().Get(ctx, slice.Name, metav1.GetOptions{})
	if getErr != nil {
		return fmt.Errorf("creating ResourceSlice: %w; fetching existing: %v", err, getErr)
	}

	slice.ResourceVersion = existing.ResourceVersion
	if slice.OwnerReferences == nil && len(existing.OwnerReferences) > 0 {
		slice.OwnerReferences = existing.OwnerReferences
	}
	_, err = sp.client.ResourceV1().ResourceSlices().Update(ctx, slice, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("updating ResourceSlice: %w", err)
	}
	klog.InfoS("Updated ResourceSlice", "name", slice.Name, "devices", len(devices))
	return nil
}

func (sp *SlicePublisher) buildDevices(partitions []*timeslot.CorePartition) []resourceapi.Device {
	allSlots := timeslot.AllSlots(partitions)
	devices := make([]resourceapi.Device, 0, len(allSlots))

	for _, slot := range allSlots {
		dev := resourceapi.Device{
			Name: slot.ID,
			Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				"core": {
					IntValue: int64Ptr(int64(slot.Core)),
				},
				"slotIndex": {
					IntValue: int64Ptr(int64(slot.Index)),
				},
				"offsetNs": {
					IntValue: int64Ptr(slot.Offset.Nanoseconds()),
				},
				"runtimeNs": {
					IntValue: int64Ptr(slot.Runtime.Nanoseconds()),
				},
				"periodNs": {
					IntValue: int64Ptr(slot.Period.Nanoseconds()),
				},
				"utilizationMillis": {
					IntValue: int64Ptr(slot.UtilizationMillis()),
				},
				"numaNode": {
					IntValue: int64Ptr(int64(slot.NUMANode)),
				},
			},
		}
		devices = append(devices, dev)
	}

	return devices
}

// SliceName returns the ResourceSlice name for this publisher.
func (sp *SlicePublisher) SliceName() string {
	return fmt.Sprintf("%s-%s", sp.nodeName, sp.driverName)
}

// DeleteSlice removes the ResourceSlice from the API server.
func (sp *SlicePublisher) DeleteSlice(ctx context.Context) error {
	name := sp.SliceName()
	err := sp.client.ResourceV1().ResourceSlices().Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("deleting ResourceSlice %s: %w", name, err)
	}
	klog.InfoS("Deleted ResourceSlice", "name", name)
	return nil
}

// nodeOwnerReference fetches the Node object and returns an owner reference
// so the ResourceSlice is garbage-collected when the node is removed.
func (sp *SlicePublisher) nodeOwnerReference(ctx context.Context) ([]metav1.OwnerReference, error) {
	node, err := sp.client.CoreV1().Nodes().Get(ctx, sp.nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting node %s: %w", sp.nodeName, err)
	}
	return []metav1.OwnerReference{
		{
			APIVersion: "v1",
			Kind:       "Node",
			Name:       node.Name,
			UID:        node.UID,
		},
	}, nil
}

func int64Ptr(v int64) *int64 {
	return &v
}
