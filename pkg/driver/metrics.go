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
)

// RegisterMetrics registers all Prometheus metrics with the default registry.
func RegisterMetrics() {
	prometheus.MustRegister(
		SlotsTotal,
		SlotsAllocated,
		PrepareTotal,
		UnprepareTotal,
	)
}
