package driver

import "github.com/prometheus/client_golang/prometheus"

const metricsNamespace = "dra_time_share"

var (
	// SlotsTotal is the total number of time slots advertised by the driver.
	SlotsTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "slots_total",
		Help:      "Total number of time slots advertised by the driver.",
	})

	// SlotsAllocated is the number of currently allocated time slots.
	SlotsAllocated = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "slots_allocated",
		Help:      "Number of currently allocated time slots.",
	})

	// ActiveWatchersGauge is the number of active cgroup watchers.
	ActiveWatchersGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "active_watchers",
		Help:      "Number of active cgroup watchers monitoring pod processes.",
	})

	// PrepareTotal counts NodePrepareResources calls.
	PrepareTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "prepare_total",
		Help:      "Total number of NodePrepareResources calls.",
	}, []string{"result"})

	// UnprepareTotal counts NodeUnprepareResources calls.
	UnprepareTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "unprepare_total",
		Help:      "Total number of NodeUnprepareResources calls.",
	}, []string{"result"})

	// SchedDeadlineApplyTotal counts SCHED_DEADLINE apply attempts.
	SchedDeadlineApplyTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "sched_deadline_apply_total",
		Help:      "Total number of SCHED_DEADLINE apply attempts on PIDs.",
	}, []string{"result"})

	// TrackedPIDs is the total number of PIDs currently tracked across all watchers.
	TrackedPIDs = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "tracked_pids",
		Help:      "Number of PIDs currently tracked with SCHED_DEADLINE scheduling.",
	})
)

// RegisterMetrics registers all Prometheus metrics with the default registry.
func RegisterMetrics() {
	prometheus.MustRegister(
		SlotsTotal,
		SlotsAllocated,
		ActiveWatchersGauge,
		PrepareTotal,
		UnprepareTotal,
		SchedDeadlineApplyTotal,
		TrackedPIDs,
	)
}
