# Deterministic Time-Share DRA Driver

A Kubernetes [Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/) driver that exposes deterministic CPU time slots using Linux's `SCHED_DEADLINE` scheduler.

Each CPU core is partitioned into non-overlapping time slots defined by an offset, runtime, and period. These slots are advertised as DRA devices, allocated to pods via ResourceClaims, and enforced at the kernel level using `sched_setattr(2)`.

## How It Works

```
Core 0, period = 1ms, 4 slots:

|----slot0----|----slot1----|----slot2----|----slot3----|
0us         250us         500us         750us        1000us
 runtime=250us runtime=250us runtime=250us runtime=250us
```

1. **Partition** — On startup, the driver divides each configured CPU core into equal time slots. Each slot gets `runtime = period / slotCount` with staggered offsets.

2. **Advertise** — Slots are published as DRA devices in a ResourceSlice. Each device exposes attributes like `core`, `runtimeNs`, `periodNs`, `numaNode`, and `utilizationMillis`.

3. **Allocate** — The Kubernetes scheduler picks a slot for each ResourceClaim. When the pod starts, kubelet calls `NodePrepareResources` and the driver records the allocation, returning CDI device IDs.

4. **Enforce** — After the container process starts, CRI-O reads the CDI spec and executes a `poststart` hook. The hook pins the container process to the slot's core via `sched_setaffinity(2)` and applies `SCHED_DEADLINE` via `sched_setattr(2)`. Using `poststart` ensures the hook fires after the container's entrypoint has been exec'd and after CRI-O's own `execCPUAffinity` step, so the single-core pin is not overwritten by the runtime.

5. **Release** — When the pod is deleted or the claim is released, `NodeUnprepareResources` frees the slot for reuse.

## Prerequisites

- **Kubernetes 1.34+** (DRA is GA since 1.34; the driver uses the `resource.k8s.io/v1` API)
- **Linux kernel without `CONFIG_RT_GROUP_SCHED`** — required for `SCHED_DEADLINE` enforcement in containers (see [Kernel Compatibility](#kernel-compatibility))
- **CRI-O or containerd** with CDI support (CDI hooks are a spec feature, not runtime-specific)
- **Go 1.26+** for building from source

### Kernel Compatibility

`SCHED_DEADLINE` requires the kernel to allow real-time scheduling in non-root cgroups. The kernel config option `CONFIG_RT_GROUP_SCHED` blocks this. Compatible kernels include:

| Platform | Kernel | Compatible? |
|----------|--------|-------------|
| RHEL / OpenShift | kernel-rt (via MachineConfig or PerformanceProfile) | Yes |
| Fedora | default kernel | Yes |
| Ubuntu | lowlatency kernel | Yes |
| Ubuntu | generic kernel | No (`CONFIG_RT_GROUP_SCHED=y`) |
| Upstream / vanilla | default | Yes |

Use [Node Feature Discovery (NFD)](#node-feature-discovery) to auto-detect compatible nodes and prevent the driver from deploying where enforcement would fail.

### CPU Isolation

`SCHED_DEADLINE` always preempts `SCHED_OTHER` (CFS), so RT containers will never lose CPU time to normal processes. However, during the **slack** interval — the portion of each period when the deadline task is sleeping (`period - runtime`) — the kernel will schedule ordinary tasks onto the same core if their affinity permits it. This causes cache pollution, TLB pressure, and memory bandwidth contention that increases wakeup latency at the start of the next period.

To eliminate this interference, configure the RT cores as **isolated CPUs** at the node level using the standard Linux mechanisms:

- `isolcpus=<cpulist>` — prevents the kernel from placing ordinary tasks on those cores
- `nohz_full=<cpulist>` — suppresses periodic timer ticks on isolated cores
- `rcu_nocbs=<cpulist>` — offloads RCU callbacks to housekeeping cores

On **RHEL / OpenShift**, these parameters are set automatically by the [Node Tuning Operator](https://docs.openshift.com/container-platform/latest/scalability_and_performance/low_latency_tuning/cnf-tuning-low-latency-nodes-with-perf-profile.html) when a **PerformanceProfile** is applied. Configure the `--cores` driver flag to match the `isolated` CPU set from the profile; the `reserved` CPUs are left for the OS and Kubernetes housekeeping.

On other platforms, set these kernel parameters manually and pass the same core list to `--cores`.

Without CPU isolation the driver still provides **bandwidth guarantees** (no RT task misses its deadline), but not full **latency isolation** from system noise.

## Project Layout

```
cmd/driver/            Main entrypoint (DRA plugin + CDI hook mode)
pkg/timeslot/          Time slot model, partitioning logic, NUMA discovery
pkg/driver/            DRA plugin, state, ResourceSlice publishing, CDI spec generation
pkg/deadline/          SCHED_DEADLINE / sched_setaffinity syscall wrappers, CDI hook
deploy/helm/           Helm chart
deployments/           Raw Kubernetes manifests
test/e2e/              End-to-end tests (kind + CRC)
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

The image is based on UBI 10 (go-toolset for build, ubi-micro for runtime). The single `dra-time-share` binary serves as both the DRA plugin and the CDI hook entry point.

## Deploy

### With Helm

```bash
helm install dra-time-share deploy/helm/dra-time-share/ \
  -n dra-time-share --create-namespace \
  --set driver.cores="4,5,6,7" \
  --set driver.slotCount=4 \
  --set driver.periodMs=1
```

### With raw manifests

```bash
kubectl apply -f deployments/rbac.yaml
kubectl apply -f deployments/device-class.yaml
kubectl apply -f deployments/node-feature-rule.yaml   # optional, requires NFD
kubectl apply -f deployments/daemonset.yaml
```

### Configuration

Edit the DaemonSet args to match your hardware:

| Flag | Default | Description |
|------|---------|-------------|
| `--cores` | (required) | Comma-separated CPU core indices to partition (e.g., `4,5,6,7`) |
| `--period-ms` | `1` | Scheduling period in milliseconds |
| `--slot-count` | `4` | Number of time slots per core |
| `--socket` | `/var/lib/kubelet/plugins/time-share.fabiendupont.io/plugin.sock` | DRA plugin socket path |
| `--cdi-dir` | `/var/run/cdi` | Directory for CDI spec files |
| `--cpu-features` | (empty) | Comma-separated allowlist of CPU flags to publish (empty = default set, `none` = disabled) |
| `--health-port` | `8080` | Port for health (`/healthz`, `/readyz`) and metrics (`/metrics`) endpoints |
| `--registry-dir` | `/var/lib/kubelet/plugins_registry` | Kubelet plugin registry directory |

## Usage

### Create a ResourceClaim

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaim
metadata:
  name: my-time-slot
spec:
  devices:
    requests:
      - name: slot
        exactly:
          deviceClassName: time-share-slots
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
| `numaNode` | int | NUMA node ID for this slot's core (-1 if unavailable) |
| `cpufreqGovernor` | string | cpufreq scaling governor (e.g. `performance`); omitted if unavailable |
| `cpufreqBaseKhz` | int | Base CPU frequency in KHz; omitted if unavailable |
| `physicalPackageId` | int | CPU socket number; omitted if unavailable |
| `feature.<name>` | bool | `true` for each CPU flag matching the allowlist (e.g. `feature.avx512f`); omitted if the flag is absent |

### Reference the claim from a Pod

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: deadline-workload
spec:
  containers:
    - name: workload
      image: my-app:latest
      command: ["/my-app"]
      resources:
        claims:
          - name: time-slot
  resourceClaims:
    - name: time-slot
      resourceClaimName: my-time-slot
```

Once the pod is running, the container's init process is pinned to the allocated core and scheduled under `SCHED_DEADLINE` with the slot's parameters.

### Verify scheduling

From the node, check the scheduling policy of the container's process:

```bash
chrt -p <pid>
```

Expected output:
```
pid <pid>'s current scheduling policy: SCHED_DEADLINE
    runtime/deadline/period parameters: <runtime>/<deadline>/<period>
```

## Comparison with dra-driver-cpu

[dra-driver-cpu](https://github.com/kubernetes-sigs/dra-driver-cpu) and this driver solve different problems and are complementary, not competing.

| | dra-driver-cpu | dra-driver-time-share |
|---|---|---|
| **Model** | Exclusive CPU allocation | Time-multiplexed CPU bandwidth |
| **Device unit** | CPU core (or NUMA/socket aggregate) | Time slot (offset + runtime within a period) |
| **Guarantee** | A core belongs to one workload at a time | A workload gets exactly `runtime` ns per `period` ns |
| **Sharing** | No — other containers are pushed to a shared pool | Yes — multiple workloads share the same core in non-overlapping slots |
| **Enforcement** | cpuset cgroup via NRI | `SCHED_DEADLINE` via CDI hook + `sched_setattr(2)` |
| **Kernel requirement** | Standard | No `CONFIG_RT_GROUP_SCHED` (kernel-rt, Fedora, upstream) |
| **Target workload** | Any workload needing guaranteed, interference-free CPUs | RT / near-RT workloads needing deterministic CPU bandwidth without owning cores outright |

Use **dra-driver-cpu** when workloads must not share cores at all. Use **dra-driver-time-share** when you need deterministic scheduling budgets and want to pack multiple latency-sensitive workloads onto the same hardware. The two drivers can coexist on the same node, managing disjoint sets of cores.

## Architecture

```
                         ┌─────────────────────────────┐
                         │        kube-scheduler       │
                         │  allocates slots from       │
                         │  ResourceSlice devices      │
                         └──────────────┬──────────────┘
                                        │
┌───────────────────────────────────────┼─────────────────────────────────┐
│ Node                                  │                                 │
│                                       │                                 │
│  ┌────────────────────────────────────▼──────────────────────────────┐  │
│  │                          kubelet                                  │  │
│  │  NodePrepareResources ──► driver records allocation               │  │
│  │                           returns CDI device IDs                  │  │
│  │                                                                   │  │
│  │  Container creation ──► CRI-O reads CDI spec                      │  │
│  │                          executes poststart hook (after exec)     │  │
│  │                          ──► hook applies                         │  │
│  │                               sched_setaffinity + sched_setattr   │  │
│  │                                                                   │  │
│  │  NodeUnprepareResources ► driver releases slot                    │  │
│  └───────────────────────────────────────────────────────────────────┘  │
│                                                                         │
│  ┌──────────────┐   ┌──────────────┐   ┌──────────────┐                 │
│  │ Allocation   │   │    Slice     │   │  CDI Specs   │                 │
│  │   State      │   │  Publisher   │   │  (/var/run/  │                 │
│  │              │   │              │   │   cdi/)      │                 │
│  │ slot→claim   │   │ ResourceSlice│   │              │                 │
│  │  mapping     │   │  with all    │   │  poststart   │                 │
│  │              │   │  devices     │   │  hooks per   │                 │
│  │              │   │  + numaNode  │   │  slot        │                 │
│  └──────────────┘   └──────────────┘   └──────────────┘                 │
└─────────────────────────────────────────────────────────────────────────┘
```

### CDI Hook Enforcement

The `dra-time-share` binary doubles as the CDI hook entry point. When invoked with `--cdi-hook`, it reads the container PID from the OCI state on stdin and calls `sched_setaffinity` + `sched_setattr` directly via Go syscalls. No external helper binaries are needed.

The hook runs as a `poststart` OCI hook, which fires after the container's entrypoint has been exec'd and is running. This ordering is deliberate: on OCP 4.22+ nodes with a PerformanceProfile, CRI-O's `execCPUAffinity` feature applies `sched_setaffinity` to the container process at exec time, pinning it to the full CPU Manager cpuset. By running as `poststart` — after exec and after `execCPUAffinity` — the hook's single-core pin always wins.

### Recovery

If the driver pod restarts, it recovers by listing all ResourceClaims from the API server and re-populating its allocation state for claims that belong to its driver and node.

## Node Feature Discovery

The driver includes a [NodeFeatureRule](deployments/node-feature-rule.yaml) that labels compatible nodes:

```yaml
time-share.fabiendupont.io/sched-deadline: "true"
```

The DaemonSet uses this label as a `nodeSelector`, ensuring the driver only deploys on nodes where `SCHED_DEADLINE` enforcement works. Install the rule alongside NFD:

```bash
kubectl apply -f deployments/node-feature-rule.yaml
```

## Topology Coordinator Integration

The driver publishes a `numaNode` attribute per device, enabling integration with the [Node Partition Topology Coordinator](https://github.com/fabiendupont/k8s-dra-topology-coordinator). A pod can claim NUMA-aligned CPU time slots alongside GPUs and NICs:

```bash
kubectl apply -f deployments/topology-rule.yaml
```

The topology rule maps `time-share.fabiendupont.io/numaNode` to the coordinator's standard NUMA model with `preferred` enforcement.

## Metrics

The driver exposes Prometheus metrics on port 8080 at `/metrics`.

| Metric | Type | Description |
|--------|------|-------------|
| `dra_time_share_slots_total` | gauge | Total number of time slots advertised |
| `dra_time_share_slots_allocated` | gauge | Number of currently allocated slots |
| `dra_time_share_prepare_total` | counter | NodePrepareResources calls (labels: `result=success\|error`) |
| `dra_time_share_unprepare_total` | counter | NodeUnprepareResources calls (labels: `result=success\|error`) |

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

### Pod stuck in `CreateContainerError`

The CDI hook failed to apply `SCHED_DEADLINE`. Check:

1. **Kernel compatibility** — The node kernel has `CONFIG_RT_GROUP_SCHED=y`. Switch to kernel-rt (OpenShift: apply a MachineConfig with `kernelType: realtime`) or use a compatible kernel.

2. **CDI spec missing** — The driver writes CDI specs to `/var/run/cdi/`. Verify:
   ```bash
   ls /var/run/cdi/time-share.fabiendupont.io-slot.json
   ```

3. **Hook binary missing** — The driver copies itself to the plugin directory at startup:
   ```bash
   ls /var/lib/kubelet/plugins/time-share.fabiendupont.io/dra-time-share-hook
   ```

### Pod stuck in Pending

Check that the ResourceClaim is allocated:

```bash
kubectl get resourceclaim my-time-slot -o yaml
```

Look for `status.allocation`. If missing, check that:
- The DeviceClass exists
- The driver DaemonSet is running and the ResourceSlice is published
- The CEL selector matches available devices
- The node has the `time-share.fabiendupont.io/sched-deadline=true` label

### Driver pod not starting

The DaemonSet requires the node label `time-share.fabiendupont.io/sched-deadline=true`. Either:
- Install NFD and apply the NodeFeatureRule, or
- Label nodes manually: `kubectl label node <name> time-share.fabiendupont.io/sched-deadline=true`

### Driver pod restarting

Check for RBAC issues:

```bash
kubectl auth can-i get resourceclaims --as=system:serviceaccount:dra-time-share:dra-time-share
kubectl auth can-i create resourceslices --as=system:serviceaccount:dra-time-share:dra-time-share
```

## E2E Tests

### kind (smoke test)

```bash
./test/e2e/run-e2e.sh
```

Requires kind, kubectl, docker/podman. Tests the DRA lifecycle (partition, publish, allocate, prepare, release) but does not verify SCHED_DEADLINE enforcement (kind nodes lack kernel capabilities).

### CRC / OpenShift (full enforcement)

```bash
./test/e2e/run-e2e-crc.sh
```

Requires a running CRC instance with kernel-rt or `sysctl kernel.sched_rt_runtime_us=-1`. Tests the complete flow including SCHED_DEADLINE enforcement via CDI hooks.

## License

See [LICENSE](LICENSE) for details.
