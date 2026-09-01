#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-dra-time-share-e2e}"
IMAGE="${IMAGE:-dra-time-share:e2e}"
CONTAINER_RUNTIME="${CONTAINER_RUNTIME:-$(command -v podman >/dev/null 2>&1 && echo podman || echo docker)}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
NAMESPACE="dra-time-share"

export KIND_EXPERIMENTAL_PROVIDER="${CONTAINER_RUNTIME}"

cleanup() {
    echo "--- Cleaning up ---"
    ${KIND_BIN} delete cluster --name "${CLUSTER_NAME}" 2>/dev/null || true
}
trap cleanup EXIT

echo "--- Building container image ---"
${CONTAINER_RUNTIME} build --network=host -t "${IMAGE}" "${ROOT_DIR}"

echo "--- Creating kind cluster ---"
KIND_BIN="${KIND_BIN:-kind}"
${KIND_BIN} create cluster --name "${CLUSTER_NAME}" --config "${SCRIPT_DIR}/kind-config.yaml" --wait 60s

echo "--- Loading images into kind ---"
ARCHIVE_FILE=$(mktemp /tmp/dra-time-share-XXXXXX.tar)
${CONTAINER_RUNTIME} save -o "${ARCHIVE_FILE}" "localhost/${IMAGE}"
${KIND_BIN} load image-archive "${ARCHIVE_FILE}" --name "${CLUSTER_NAME}"
rm -f "${ARCHIVE_FILE}"

# Pre-load busybox for the test pod (kind nodes may lack internet access).
${CONTAINER_RUNTIME} pull docker.io/library/busybox:latest 2>/dev/null || true
BUSYBOX_ARCHIVE=$(mktemp /tmp/busybox-XXXXXX.tar)
${CONTAINER_RUNTIME} save -o "${BUSYBOX_ARCHIVE}" docker.io/library/busybox:latest
${KIND_BIN} load image-archive "${BUSYBOX_ARCHIVE}" --name "${CLUSTER_NAME}"
rm -f "${BUSYBOX_ARCHIVE}"

echo "--- Labeling nodes for SCHED_DEADLINE ---"
kubectl label nodes --all time-share.fabiendupont.io/sched-deadline=true

echo "--- Applying RBAC ---"
kubectl create namespace "${NAMESPACE}"
kubectl apply -f "${ROOT_DIR}/deployments/rbac.yaml"

echo "--- Applying DeviceClass ---"
kubectl apply -f "${ROOT_DIR}/deployments/device-class.yaml"

echo "--- Deploying DaemonSet ---"
cat "${ROOT_DIR}/deployments/daemonset.yaml" | \
    sed "s|image: quay.io/fabiendupont/dra-time-share:latest|image: localhost/${IMAGE}\n          imagePullPolicy: Never|" | \
    sed 's|--cores=0,1,2,3|--cores=0|' | \
    sed 's|--slot-count=4|--slot-count=2|' | \
    kubectl apply -f -

echo "--- Waiting for DaemonSet to be ready ---"
kubectl -n "${NAMESPACE}" rollout status daemonset/dra-time-share --timeout=120s || {
    echo "FAIL: DaemonSet not ready"
    kubectl -n "${NAMESPACE}" get pods -o wide
    kubectl -n "${NAMESPACE}" describe pods
    kubectl -n "${NAMESPACE}" logs -l app=dra-time-share --tail=50 || true
    exit 1
}

echo "--- Verifying ResourceSlice ---"
SLICE_COUNT=$(kubectl get resourceslices -o json | \
    jq '[.items[] | select(.spec.driver == "time-share.fabiendupont.io")] | length')
if [ "${SLICE_COUNT}" -eq 0 ]; then
    echo "FAIL: No ResourceSlice found for time-share driver"
    kubectl -n "${NAMESPACE}" logs -l app=dra-time-share --tail=50
    exit 1
fi
echo "OK: Found ${SLICE_COUNT} ResourceSlice(s)"

echo "--- Verifying device attributes ---"
DEVICE_COUNT=$(kubectl get resourceslices -o json | \
    jq '[.items[] | select(.spec.driver == "time-share.fabiendupont.io") | .spec.devices[]] | length')
if [ "${DEVICE_COUNT}" -ne 2 ]; then
    echo "FAIL: Expected 2 devices (1 core x 2 slots), got ${DEVICE_COUNT}"
    exit 1
fi
echo "OK: Found ${DEVICE_COUNT} devices"

echo "--- Creating ResourceClaim ---"
kubectl apply -f "${ROOT_DIR}/deployments/examples/claim.yaml"

echo "--- Creating test pod ---"
cat "${ROOT_DIR}/deployments/examples/pod.yaml" | \
    sed 's|image: busybox:latest|image: docker.io/library/busybox:latest\n      imagePullPolicy: IfNotPresent|' | \
    kubectl apply -f -

echo "--- Waiting for pod to be running ---"
kubectl wait --for=condition=Ready pod/deadline-workload --timeout=60s || {
    echo "FAIL: Pod did not become ready"
    kubectl describe pod/deadline-workload
    kubectl -n "${NAMESPACE}" logs -l app=dra-time-share --tail=50
    exit 1
}

echo "--- Verifying claim preparation ---"
PREPARE_COUNT=$(kubectl -n "${NAMESPACE}" logs -l app=dra-time-share | \
    grep -c "Prepared claim for SCHED_DEADLINE scheduling" || true)
if [ "${PREPARE_COUNT}" -eq 0 ]; then
    echo "FAIL: No claim preparation found in driver logs"
    kubectl -n "${NAMESPACE}" logs -l app=dra-time-share --tail=50
    exit 1
fi
echo "OK: Found ${PREPARE_COUNT} claim preparation(s) in driver logs"

echo "--- Checking metrics endpoint ---"
DRIVER_POD=$(kubectl -n "${NAMESPACE}" get pods -l app=dra-time-share -o jsonpath='{.items[0].metadata.name}')
METRICS=$(kubectl -n "${NAMESPACE}" exec "${DRIVER_POD}" -- wget -qO- http://localhost:8080/metrics 2>/dev/null || true)
if echo "${METRICS}" | grep -q "dra_time_share_slots_total"; then
    echo "OK: Metrics endpoint is serving"
else
    echo "WARN: Could not verify metrics endpoint"
fi

echo "--- Cleaning up test resources ---"
kubectl delete pod/deadline-workload --grace-period=0 --force 2>/dev/null || true
kubectl delete resourceclaim/my-time-slot 2>/dev/null || true

sleep 2
RELEASE_COUNT=$(kubectl -n "${NAMESPACE}" logs -l app=dra-time-share | \
    grep -c "Unprepared claim, released slots" || true)
if [ "${RELEASE_COUNT}" -gt 0 ]; then
    echo "OK: Slot released after pod deletion"
else
    echo "WARN: Could not verify slot release"
fi

echo ""
echo "=== E2E tests PASSED ==="
