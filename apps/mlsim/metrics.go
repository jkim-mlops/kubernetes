package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics go through client_golang rather than a hand-printed exposition
// format. The reason is not tidiness: a histogram is a real metric type with
// real quantiles, and the scenarios that scale on latency — KEDA's Prometheus
// scaler, a custom-metrics adapter — need one. Registering with the default
// registry also brings the go_* and process_* collectors along, which is how
// the memory scenarios show RSS climbing towards the limit before the OOMKill.

var (
	metricRequests = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mlsim_requests_total",
		Help: "Inference requests handled.",
	})
	metricErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mlsim_errors_total",
		Help: "Inference requests that failed.",
	})
	metricTimeouts = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mlsim_timeouts_total",
		Help: "Requests that gave up waiting for a worker.",
	})
	metricInflight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mlsim_inflight",
		Help: "Requests currently in flight.",
	})
	metricQueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mlsim_queue_depth",
		Help: "Last observed depth of the inference queue.",
	})
	metricModelLoad = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mlsim_model_load_seconds",
		Help: "Time the model took to load.",
	})

	// Queue wait is included on purpose: this is what a caller experiences,
	// and the gap between it and LATENCY_MS is the backlog. Buckets run from
	// 50ms to ~25s, which spans a healthy response and a gateway timeout.
	metricRequestSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "mlsim_request_duration_seconds",
		Help:    "End-to-end latency of an inference request, queue wait included.",
		Buckets: prometheus.ExponentialBuckets(0.05, 2, 10),
	})

	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "mlsim_ready",
		Help: "1 when the model is loaded and serving.",
	}, func() float64 {
		if ready.Load() {
			return 1
		}
		return 0
	})
)
