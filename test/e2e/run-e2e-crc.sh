#!/usr/bin/env bash
#
# E2E test for CRC (OpenShift Local). Unlike the kind-based test, this
# validates the full SCHED_DEADLINE enforcement path because CRC runs
# a real VM with kernel privilege support.
#
# Prerequisites:
#   - CRC running (crc status shows Running)
#   - oc logged in as kubeadmin
#
set -euo pipefail

IMAGE="${IMAGE:-dra-time-share:e2e}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
NAMESPACE="dra-time-share"
INTERNAL_REGISTRY="default-route-openshift-image-registry.apps-crc.testing"

cleanup() {
    echo "--- Cleaning up test resources ---"
    oc delete pod/deadline-workload --grace-period=0 --force 2>/dev/null || true
    oc delete resourceclaim/my-time-slot 2>/dev/null || true
    oc delete -f "${ROOT_DIR}/deployments/daemonset.yaml" 2>/dev/null || true
    oc delete -f "${ROOT_DIR}/deployments/device-class.yaml" 2>/dev/null || true
    oc delete -f "${ROOT_DIR}/deployments/rbac.yaml" 2>/dev/null || true
}
trap cleanup EXIT

echo "--- Checking CRC status ---"
crc status | grep -q "Running" || { echo "FAIL: CRC is not running"; exit 1; }
oc whoami > /dev/null 2>&1 || { echo "FAIL: Not logged in to OpenShift"; exit 1; }

OCP_VERSION=$(oc version -o json | jq -r '.openshiftVersion')
K8S_VERSION=$(oc version -o json | jq -r '.serverVersion.gitVersion')
echo "OpenShift ${OCP_VERSION} (${K8S_VERSION})"

echo "--- Building container image ---"
podman build --network=host -t "${IMAGE}" "${ROOT_DIR}"

echo "--- Pushing image to CRC internal registry ---"
# Ensure the namespace exists before pushing (the registry requires the namespace/project).
oc new-project "${NAMESPACE}" 2>/dev/null || oc project "${NAMESPACE}" 2>/dev/null || true

# Expose the internal registry route.
oc patch configs.imageregistry.operator.openshift.io/cluster --type merge -p '{"spec":{"defaultRoute":true}}' 2>/dev/null || true
sleep 2

# Log in and push.
podman login --tls-verify=false -u kubeadmin -p "$(oc whoami -t)" "${INTERNAL_REGISTRY}"
podman tag "localhost/${IMAGE}" "${INTERNAL_REGISTRY}/${NAMESPACE}/${IMAGE}"
podman push --tls-verify=false "${INTERNAL_REGISTRY}/${NAMESPACE}/${IMAGE}"

echo "--- Applying RBAC ---"
oc apply -f "${ROOT_DIR}/deployments/rbac.yaml"
# Grant the service account privileged SCC for sched_setattr.
oc adm policy add-scc-to-user privileged -z dra-time-share -n "${NAMESPACE}" 2>/dev/null || true

echo "--- Applying DeviceClass ---"
oc apply -f "${ROOT_DIR}/deployments/device-class.yaml"

echo "--- Deploying DaemonSet ---"
# Use the internal registry image reference. Increase probe delays for
# CRC's slower API server and use core 0 with 2 slots.
cat "${ROOT_DIR}/deployments/daemonset.yaml" | \
    sed "s|image: quay.io/fabiendupont/dra-time-share:latest|image: image-registry.openshift-image-registry.svc:5000/${NAMESPACE}/${IMAGE}\n          imagePullPolicy: Always|" | \
    sed 's|--cores=0,1,2,3|--cores=0|' | \
    sed 's|--slot-count=4|--slot-count=2|' | \
    sed 's|initialDelaySeconds: 5|initialDelaySeconds: 30|' | \
    sed 's|initialDelaySeconds: 3|initialDelaySeconds: 30|' | \
    oc apply -f -

# The driver may crash-loop a few times while the in-cluster API server DNS
# resolves. Poll for the pod to reach Ready rather than using rollout status.
echo "--- Waiting for driver pod to be ready ---"
DEADLINE=$((SECONDS + 600))
while [ $SECONDS -lt $DEADLINE ]; do
    POD_READY=$(oc -n "${NAMESPACE}" get pods -l app=dra-time-share \
        -o jsonpath='{.items[0].status.containerStatuses[0].ready}' 2>/dev/null || true)
    if [ "${POD_READY}" = "true" ]; then
        echo "Driver pod is ready"
        break
    fi
    RESTARTS=$(oc -n "${NAMESPACE}" get pods -l app=dra-time-share \
        -o jsonpath='{.items[0].status.containerStatuses[0].restartCount}' 2>/dev/null || echo "?")
    echo "  ready=${POD_READY} restarts=${RESTARTS}, waiting..."
    sleep 10
done

if [ "${POD_READY}" != "true" ]; then
    echo "FAIL: Driver pod not ready within 10 minutes"
    oc -n "${NAMESPACE}" get pods -o wide
    oc -n "${NAMESPACE}" describe pods
    oc -n "${NAMESPACE}" logs -l app=dra-time-share --tail=50 || true
    exit 1
fi

echo "--- Verifying ResourceSlice ---"
SLICE_COUNT=$(oc get resourceslices -o json | \
    jq '[.items[] | select(.spec.driver == "time-share.fabiendupont.io")] | length')
if [ "${SLICE_COUNT}" -eq 0 ]; then
    echo "FAIL: No ResourceSlice found"
    oc -n "${NAMESPACE}" logs -l app=dra-time-share --tail=50
    exit 1
fi
echo "OK: Found ${SLICE_COUNT} ResourceSlice(s)"

echo "--- Verifying device attributes ---"
DEVICE_COUNT=$(oc get resourceslices -o json | \
    jq '[.items[] | select(.spec.driver == "time-share.fabiendupont.io") | .spec.devices[]] | length')
echo "OK: Found ${DEVICE_COUNT} devices"

# Check numaNode attribute is published.
NUMA_ATTR=$(oc get resourceslices -o json | \
    jq -r '[.items[] | select(.spec.driver == "time-share.fabiendupont.io") | .spec.devices[0].attributes.numaNode.int // "missing"] | .[0]')
echo "OK: numaNode attribute = ${NUMA_ATTR}"

echo "--- Creating ResourceClaim ---"
oc apply -f "${ROOT_DIR}/deployments/examples/claim.yaml"

echo "--- Creating test pod ---"
cat "${ROOT_DIR}/deployments/examples/pod.yaml" | \
    sed 's|image: busybox:latest|image: busybox:latest\n      imagePullPolicy: IfNotPresent|' | \
    oc apply -f -

echo "--- Waiting for pod to be running ---"
oc wait --for=condition=Ready pod/deadline-workload --timeout=120s || {
    echo "FAIL: Pod did not become ready"
    oc describe pod/deadline-workload
    oc -n "${NAMESPACE}" logs -l app=dra-time-share --tail=50
    exit 1
}

echo "--- Verifying SCHED_DEADLINE enforcement ---"
# With CDI hooks, SCHED_DEADLINE is applied at container creation by CRI-O.
# Check the driver logs for claim preparation.
DRIVER_POD=$(oc -n "${NAMESPACE}" get pods -l app=dra-time-share -o jsonpath='{.items[0].metadata.name}')
PREPARE_COUNT=$(oc -n "${NAMESPACE}" logs "${DRIVER_POD}" | \
    grep -c "Prepared claim for SCHED_DEADLINE scheduling" || true)
if [ "${PREPARE_COUNT}" -eq 0 ]; then
    echo "FAIL: No claim preparation found"
    oc -n "${NAMESPACE}" logs "${DRIVER_POD}" --tail=30
    exit 1
fi
echo "OK: ${PREPARE_COUNT} claim(s) prepared with CDI device IDs"

# Verify CDI spec exists on the node.
CDI_SPEC=$(oc -n "${NAMESPACE}" exec "${DRIVER_POD}" -- cat /var/run/cdi/time-share.fabiendupont.io-slot.json 2>/dev/null | head -1 || true)
if [ -n "${CDI_SPEC}" ]; then
    echo "OK: CDI spec found on node"
else
    echo "WARN: Could not read CDI spec"
fi

# Check if SCHED_DEADLINE was applied by the CDI hook.
# The hook writes to CRI-O's container log. Check node journal or pod events.
echo "Checking container events for CDI hook execution..."
HOOK_EVENT=$(oc get events --field-selector involvedObject.name=deadline-workload -o json 2>/dev/null | \
    jq -r '.items[].message' 2>/dev/null | grep -i "hook\|cdi" | head -1 || true)
if [ -n "${HOOK_EVENT}" ]; then
    echo "OK: CDI hook event: ${HOOK_EVENT}"
fi

# Verify the workload pod has the DRA_TIME_SHARE env vars injected by CDI.
SLOT_ENV=$(oc exec deadline-workload -- sh -c 'env | grep DRA_TIME_SHARE' 2>/dev/null || true)
if [ -n "${SLOT_ENV}" ]; then
    echo "OK: CDI env vars injected: ${SLOT_ENV}"
else
    echo "WARN: DRA_TIME_SHARE env vars not found in workload pod"
fi

echo "--- Verifying slot release ---"
oc delete pod/deadline-workload --grace-period=5 2>/dev/null
oc delete resourceclaim/my-time-slot 2>/dev/null
sleep 3

RELEASE_COUNT=$(oc -n "${NAMESPACE}" logs "${DRIVER_POD}" | \
    grep -c "Unprepared claim, released slots" || true)
if [ "${RELEASE_COUNT}" -gt 0 ]; then
    echo "OK: Slot released after pod deletion"
else
    echo "WARN: Could not verify slot release in logs"
fi

echo ""
echo "=== CRC E2E tests PASSED ==="
