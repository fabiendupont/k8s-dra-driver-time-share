# Deterministic Time-Share DRA Driver

A Kubernetes [Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/) driver that exposes deterministic CPU time slots using Linux's `SCHED_DEADLINE` scheduler.

Each CPU core is partitioned into non-overlapping time slots defined by an offset, runtime, and period. These slots are advertised as DRA devices, allocated to pods via ResourceClaims, and enforced at the kernel level using `sched_setattr(2)`.

## How It Works

```
Core 0, period = 1ms, 4 slots:

|----slot0----|----slot1----|----slot2----|----slot3----|
0µs         250µs         500µs         750µs        1000µs
 runtime=250µs runtime=250µs runtime=250µs runtime=250µs
```

1. **Partition** — On startup, the driver divides each configured CPU core into equal time slots. Each slot gets `runtime = period / slotCount` with staggered offsets.

2. **Advertise** — Slots are published as DRA devices in a ResourceSlice. Each device exposes attributes like `core`, `runtimeNs`, `periodNs`, and `utilizationMillis`.

3. **Allocate** — The Kubernetes scheduler picks a slot for each ResourceClaim. When the pod starts, kubelet calls `NodePrepareResources` and the driver records the allocation.

4. **Enforce** — A pod watcher detects running pods with allocated claims, resolves their cgroup paths, and starts polling for PIDs. Each PID is pinned to the slot's core via `sched_setaffinity(2)` and given a `SCHED_DEADLINE` budget via `sched_setattr(2)`.

5. **Release** — When the pod is deleted or the claim is released, the driver clears `SCHED_DEADLINE` from all tracked PIDs and frees the slot.

## Prerequisites

- **Kubernetes 1.32+** with the `DynamicResourceAllocation` feature gate enabled
- **Linux kernel 3.14+** with `SCHED_DEADLINE` support (all modern kernels)
- **cgroup v2** — the driver resolves pod cgroup paths under `/sys/fs/cgroup`
- **Privileged container** — `sched_setattr(2)` requires `CAP_SYS_NICE` (the DaemonSet runs privileged)
- **Go 1.23+** for building from source

## Project Layout

```
cmd/driver/            Main entrypoint
pkg/timeslot/          Time slot model and partitioning logic
pkg/driver/            DRA plugin, state, ResourceSlice publishing,
                       pod/cgroup watchers, recovery
pkg/deadline/          SCHED_DEADLINE and sched_setaffinity syscall wrappers
deployments/           Kubernetes manifests
deployments/examples/  Example ResourceClaim and Pod
```

## Build

```bash
make build      # build binary to bin/dra-time-share
make test       # run unit tests
make image      # build container image with podman
```

The container image defaults to `quay.io/fabiendupont/dra-time-share:latest`. Override with:

```bash
make image IMAGE=my-registry/dra-time-share TAG=v0.1.0
```

## Deploy

### 1. Create the namespace, ServiceAccount, and RBAC

```bash
kubectl apply -f deployments/rbac.yaml
```

This creates:
- Namespace `dra-time-share`
- ServiceAccount with ClusterRole permissions for ResourceSlices (CRUD), ResourceClaims (GET), and Pods (GET/LIST/WATCH)

### 2. Create the DeviceClass

```bash
kubectl apply -f deployments/device-class.yaml
```

The `time-share-slots` DeviceClass selects all devices from driver `time-share.fabiendupont.io`.

### 3. Deploy the DaemonSet

```bash
kubectl apply -f deployments/daemonset.yaml
```

Edit the DaemonSet args to match your hardware:

| Flag | Default | Description |
|------|---------|-------------|
| `--cores` | (required) | Comma-separated CPU core indices to partition (e.g., `0,1,2,3`) |
| `--period-ms` | `1` | Scheduling period in milliseconds |
| `--slot-count` | `4` | Number of time slots per core |
| `--cgroup-root` | `/sys/fs/cgroup` | cgroup v2 root path |
| `--socket` | `/var/lib/kubelet/plugins/time-share.fabiendupont.io/plugin.sock` | DRA plugin socket path |
| `--health-port` | `8080` | Port for health (`/healthz`, `/readyz`) and metrics (`/metrics`) endpoints |
| `--registry-dir` | `/var/lib/kubelet/plugins_registry` | Kubelet plugin registry directory |

## Usage

### Create a ResourceClaim

```yaml
apiVersion: resource.k8s.io/v1beta1
kind: ResourceClaim
metadata:
  name: my-time-slot
spec:
  devices:
    requests:
      - name: slot
        deviceClassName: time-share-slots
        count: 1
        selectors:
          - cel:
              expression: >-
                device.attributes['time-share.fabiendupont.io'].utilizationMillis >= 250
```

The CEL selector above requests a slot with at least 25% CPU utilization (250 = 25.0%).

### Available device attributes

All attributes are in the `time-share.fabiendupont.io` domain. Access them in CEL as `device.attributes['time-share.fabiendupont.io'].<name>`.

| Attribute | Type | Description |
|-----------|------|-------------|
| `core` | int | CPU core index |
| `slotIndex` | int | Slot index within the core (0-based) |
| `offsetNs` | int | Start offset within each period (nanoseconds) |
| `runtimeNs` | int | Guaranteed runtime per period (nanoseconds) |
| `periodNs` | int | Scheduling period (nanoseconds) |
| `utilizationMillis` | int | CPU utilization in tenths of a percent (250 = 25.0%) |

### Reference the claim from a Pod

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: deadline-workload
spec:
  containers:
    - name: workload
      image: busybox:latest
      command: ["sh", "-c", "while true; do echo 'running'; sleep 1; done"]
      resources:
        claims:
          - name: time-slot
  resourceClaims:
    - name: time-slot
      resourceClaimName: my-time-slot
```

Once the pod is running, all its processes will be pinned to the allocated core and scheduled under `SCHED_DEADLINE` with the slot's parameters.

### Verify scheduling

From inside the pod or on the node, check the scheduling policy of a process:

```bash
chrt -p <pid>
```

Expected output:
```
pid <pid>'s current scheduling policy: SCHED_DEADLINE
    runtime/deadline/period parameters: <runtime>/<deadline>/<period>
```

## Architecture

```
┌───────────────────────────────────────────────────────┐
│                    kubelet                            │
│  NodePrepareResources ──► Driver.prepareClaim()       │
│  NodeUnprepareResources ► Driver.unprepareClaim()     │
└────────────────────────────┬──────────────────────────┘
                             │
          ┌──────────────────┼──────────────────┐
          │                  │                  │
   ┌──────▼──────┐   ┌───────▼──────┐   ┌───────▼───────┐
   │ Allocation  │   │    Pod       │   │   Slice       │
   │   State     │   │  Watcher     │   │ Publisher     │
   │             │   │  (informer)  │   │               │
   │ slot→claim  │   │              │   │ ResourceSlice │
   │   mapping   │   │ detects      │   │  with all     │
   │             │   │ running pods │   │  devices      │
   └─────────────┘   └───────┬──────┘   └───────────────┘
                             │
                     ┌───────▼──────┐
                     │   Cgroup     │
                     │   Watcher    │
                     │              │
                     │ polls PIDs   │
                     │ applies      │
                     │ SCHED_       │
                     │ DEADLINE     │
                     └──────────────┘
```

### Recovery

If the driver pod restarts, it recovers by listing all ResourceClaims from the API server and re-populating its allocation state for claims that belong to its driver and node. The pod watcher then re-discovers running pods and re-applies scheduling.

### Cgroup Path Resolution

The driver supports three cgroup v2 layouts for locating pod cgroups:

| Layout | Path pattern |
|--------|-------------|
| **systemd** (default) | `<root>/kubepods.slice/kubepods-<qos>.slice/kubepods-<qos>-pod<uid>.slice/` |
| **cgroupfs** | `<root>/kubepods/<qos>/pod<uid>/` |
| **kubelet.slice** | `<root>/kubelet.slice/kubelet-kubepods.slice/kubelet-kubepods-<qos>.slice/kubelet-kubepods-<qos>-pod<uid>.slice/` |

## Metrics

The driver exposes Prometheus metrics on port 8080 at `/metrics`.

| Metric | Type | Description |
|--------|------|-------------|
| `dra_time_share_slots_total` | gauge | Total number of time slots advertised |
| `dra_time_share_slots_allocated` | gauge | Number of currently allocated slots |
| `dra_time_share_active_watchers` | gauge | Number of active cgroup watchers |
| `dra_time_share_tracked_pids` | gauge | Number of PIDs with SCHED_DEADLINE applied |
| `dra_time_share_prepare_total` | counter | NodePrepareResources calls (labels: `result=success\|error`) |
| `dra_time_share_unprepare_total` | counter | NodeUnprepareResources calls (labels: `result=success\|error`) |
| `dra_time_share_sched_deadline_apply_total` | counter | SCHED_DEADLINE apply attempts (labels: `result=success\|error`) |

To scrape with Prometheus, add a `PodMonitor` or annotate the pods:

```yaml
metadata:
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "8080"
    prometheus.io/path: "/metrics"
```

## Configuration Examples

### Fewer slots, more CPU per slot

4 cores with 2 slots each — each slot gets 50% of a core:

```yaml
args:
  - --cores=0,1,2,3
  - --period-ms=1
  - --slot-count=2
```

### More slots, finer granularity

2 cores with 10 slots each — each slot gets 10% of a core:

```yaml
args:
  - --cores=0,1
  - --period-ms=1
  - --slot-count=10
```

### Isolating specific cores

Only partition cores 4-7, leaving cores 0-3 for the OS and Kubernetes:

```yaml
args:
  - --cores=4,5,6,7
  - --period-ms=1
  - --slot-count=4
```

## Troubleshooting

### Pod stuck in Pending

Check that the ResourceClaim is allocated:

```bash
kubectl get resourceclaim my-time-slot -o yaml
```

Look for `status.allocation`. If missing, check that:
- The DeviceClass exists
- The driver DaemonSet is running and the ResourceSlice is published
- The CEL selector matches available devices

```bash
kubectl get resourceslice -o yaml
```

### SCHED_DEADLINE not applied

Check the driver logs:

```bash
kubectl logs -n dra-time-share -l app=dra-time-share
```

Common issues:
- **"Could not resolve cgroup path"** — The pod's cgroup layout doesn't match any of the supported patterns. Check `--cgroup-root`.
- **"sched_setattr: operation not permitted"** — The driver container isn't running privileged or lacks `CAP_SYS_NICE`.
- **"sched_setattr: invalid argument"** — The runtime/period values may be too small for the kernel. The minimum `SCHED_DEADLINE` runtime is typically 1024ns.

### Driver pod restarting

Check for RBAC issues:

```bash
kubectl auth can-i get resourceclaims --as=system:serviceaccount:dra-time-share:dra-time-share
kubectl auth can-i list pods --as=system:serviceaccount:dra-time-share:dra-time-share
```

## License

See [LICENSE](LICENSE) for details.
