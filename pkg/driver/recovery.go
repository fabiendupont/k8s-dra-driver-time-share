package driver

import (
	"context"
	"fmt"

	resourceapi "k8s.io/api/resource/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const recoveryPageSize = 500

// RecoverAllocations re-populates the allocation state by listing all
// ResourceClaims that have devices allocated by this driver on this node.
// Uses pagination to handle large clusters. This is called on startup so
// the driver can resume after a restart without losing track of existing
// allocations.
func RecoverAllocations(ctx context.Context, client kubernetes.Interface, driverName, nodeName string, state *AllocationState) (int, error) {
	var recovered, totalClaims int
	continueToken := ""

	for {
		claims, err := client.ResourceV1beta1().ResourceClaims("").List(ctx, metav1.ListOptions{
			Limit:    recoveryPageSize,
			Continue: continueToken,
		})
		if err != nil {
			return recovered, fmt.Errorf("listing ResourceClaims: %w", err)
		}

		totalClaims += len(claims.Items)
		for i := range claims.Items {
			claim := &claims.Items[i]
			n := recoverClaim(claim, driverName, nodeName, state)
			recovered += n
		}

		continueToken = claims.Continue
		if continueToken == "" {
			break
		}
	}

	if recovered > 0 {
		klog.InfoS("Recovered allocations from existing ResourceClaims",
			"claimsScanned", totalClaims, "slots", recovered)
	}

	return recovered, nil
}

// recoverClaim checks a single ResourceClaim for devices allocated by our
// driver on our node and records them in the allocation state.
func recoverClaim(claim *resourceapi.ResourceClaim, driverName, nodeName string, state *AllocationState) int {
	if claim.Status.Allocation == nil {
		return 0
	}

	// Only recover claims that are allocated on our node.
	if !claimAllocatedOnNode(claim, nodeName) {
		return 0
	}

	claimUID := string(claim.UID)
	var count int
	for _, result := range claim.Status.Allocation.Devices.Results {
		if result.Driver != driverName {
			continue
		}
		if result.Pool != nodeName {
			continue
		}
		if err := state.Allocate(result.Device, claimUID); err != nil {
			klog.V(3).InfoS("Skipping device during recovery",
				"device", result.Device, "claim", claimUID, "error", err)
			continue
		}
		count++
	}

	if count > 0 {
		klog.InfoS("Recovered claim allocation",
			"claim", claimUID,
			"namespace", claim.Namespace,
			"name", claim.Name,
			"slots", count)
	}
	return count
}

// claimAllocatedOnNode checks whether a ResourceClaim has any devices
// allocated from the given node's pool.
func claimAllocatedOnNode(claim *resourceapi.ResourceClaim, nodeName string) bool {
	if claim.Status.Allocation == nil {
		return false
	}
	for _, result := range claim.Status.Allocation.Devices.Results {
		if result.Pool == nodeName {
			return true
		}
	}
	return false
}
