# k8s-dra-driver-deterministic-time-share

## Overview

Kubernetes DRA (Dynamic Resource Allocation) driver that exposes deterministic
CPU time slots using Linux's SCHED_DEADLINE scheduler. Cores are partitioned
into non-overlapping time slots (offset + runtime within a period), advertised
as ResourceSlices, and allocated to pods via ResourceClaims.

## Project Layout

- `cmd/driver/` - DRA driver entrypoint
- `pkg/timeslot/` - Time slot data model and partitioning logic
- `pkg/driver/` - DRA kubelet plugin (gRPC NodeServer), state, ResourceSlice publishing
- `pkg/deadline/` - SCHED_DEADLINE syscall wrappers (sched_setattr)
- `deployments/` - Kubernetes manifests (DaemonSet, DeviceClass, examples)

## Driver Name

`time-share.fabiendupont.io`

## Build

```bash
make build      # build binary
make test       # run tests
make image      # build container image
```

## Key Design Decisions

- Time slots are the atomic unit: one slot = one device in DRA terms
- Each slot is identified as `core<N>-slot<M>`
- Slots within a core share the same period; runtime = period / slotCount
- Offsets are staggered to avoid overlap: offset_i = i * runtime
- SCHED_DEADLINE is applied via sched_setattr(2) on container PIDs
- The driver watches cgroups to catch newly spawned processes in allocated pods
