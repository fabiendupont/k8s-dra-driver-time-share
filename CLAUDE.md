# k8s-dra-driver-time-share

## Overview

Kubernetes DRA (Dynamic Resource Allocation) driver that exposes deterministic
CPU time slots using Linux's SCHED_DEADLINE scheduler. Cores are partitioned
into non-overlapping time slots (offset + runtime within a period), advertised
as ResourceSlices, and allocated to pods via ResourceClaims.

SCHED_DEADLINE is enforced at container creation via CDI hooks — CRI-O
executes a `createRuntime` hook that calls `sched_setaffinity` +
`sched_setattr` on the container PID via Go syscalls.

## Project Layout

- `cmd/driver/` - DRA driver entrypoint (also CDI hook mode via `--cdi-hook`)
- `pkg/timeslot/` - Time slot data model, partitioning logic, NUMA discovery
- `pkg/driver/` - DRA kubelet plugin (gRPC NodeServer), state, ResourceSlice publishing, CDI spec generation
- `pkg/deadline/` - SCHED_DEADLINE syscall wrappers, CDI hook entry point
- `deploy/helm/` - Helm chart
- `deployments/` - Raw Kubernetes manifests (DaemonSet, DeviceClass, RBAC, NFD rule, examples)

## Driver Name

`time-share.fabiendupont.io`

## Build

```bash
make build      # build binary
make test       # run tests
make image      # build container image
```

## Helm

```bash
helm install dra-time-share deploy/helm/dra-time-share/ -n dra-time-share --create-namespace
```

## E2E Tests

```bash
./test/e2e/run-e2e.sh        # kind (smoke test)
./test/e2e/run-e2e-crc.sh    # CRC/OpenShift (full enforcement)
```

## Key Design Decisions

- Time slots are the atomic unit: one slot = one device in DRA terms
- Each slot is identified as `core<N>-slot<M>`
- Slots within a core share the same period; runtime = period / slotCount
- Offsets are staggered to avoid overlap: offset_i = i * runtime
- SCHED_DEADLINE is applied via CDI `createRuntime` hooks at container creation, using Go syscalls directly
- The driver requires a kernel without CONFIG_RT_GROUP_SCHED (kernel-rt on RHEL/OpenShift, default on Fedora/upstream)
- NFD NodeFeatureRule auto-detects compatible nodes; DaemonSet only deploys where enforcement works
- NUMA node membership is discovered from sysfs and published as a device attribute for topology coordinator integration
