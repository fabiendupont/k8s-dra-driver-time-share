package driver

import (
	"context"
	"fmt"

	resourceapi "k8s.io/api/resource/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/fabiendupont/k8s-dra-driver-deterministic-time-share/pkg/timeslot"
)

// SlicePublisher publishes and updates ResourceSlices for time slot devices.
type SlicePublisher struct {
	client     kubernetes.Interface
	driverName string
	nodeName   string
	state      *AllocationState
}

// NewSlicePublisher creates a publisher that manages ResourceSlices.
func NewSlicePublisher(client kubernetes.Interface, driverName, nodeName string, state *AllocationState) *SlicePublisher {
	return &SlicePublisher{
		client:     client,
		driverName: driverName,
		nodeName:   nodeName,
		state:      state,
	}
}

// PublishSlices creates or updates the ResourceSlice for available time slots.
func (sp *SlicePublisher) PublishSlices(ctx context.Context, partitions []*timeslot.CorePartition) error {
	devices := sp.buildDevices(partitions)

	slice := &resourceapi.ResourceSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", sp.nodeName, sp.driverName),
		},
		Spec: resourceapi.ResourceSliceSpec{
			Driver:   sp.driverName,
			NodeName: sp.nodeName,
			Pool: resourceapi.ResourcePool{
				Name:               sp.nodeName,
				ResourceSliceCount: 1,
			},
			Devices: devices,
		},
	}

	existing, err := sp.client.ResourceV1beta1().ResourceSlices().Get(ctx, slice.Name, metav1.GetOptions{})
	if err != nil {
		_, createErr := sp.client.ResourceV1beta1().ResourceSlices().Create(ctx, slice, metav1.CreateOptions{})
		if createErr != nil {
			return fmt.Errorf("creating ResourceSlice: %w", createErr)
		}
		klog.InfoS("Created ResourceSlice", "name", slice.Name, "devices", len(devices))
		return nil
	}

	slice.ResourceVersion = existing.ResourceVersion
	_, err = sp.client.ResourceV1beta1().ResourceSlices().Update(ctx, slice, metav1.UpdateOptions{})
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
			Basic: &resourceapi.BasicDevice{
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
				},
			},
		}
		devices = append(devices, dev)
	}

	return devices
}

func int64Ptr(v int64) *int64 {
	return &v
}
