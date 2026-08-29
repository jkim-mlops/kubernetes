package main

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

// A hand-rolled Prometheus text endpoint. Four counters and two gauges do not
// justify a dependency, and the exposition format is stable.
var (
	metricRequests  atomic.Int64
	metricErrors    atomic.Int64
	metricTimeouts  atomic.Int64
	metricInflight  atomic.Int64
	metricQueueDeep atomic.Int64
	metricModelLoad atomic.Int64 // seconds spent loading the model
)

func handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	emit := func(name, typ, help string, value int64) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n%s %d\n", name, help, name, typ, name, value)
	}

	emit("mlsim_requests_total", "counter", "Inference requests handled.", metricRequests.Load())
	emit("mlsim_errors_total", "counter", "Inference requests that failed.", metricErrors.Load())
	emit("mlsim_timeouts_total", "counter", "Requests that gave up waiting for a worker.", metricTimeouts.Load())
	emit("mlsim_inflight", "gauge", "Requests currently in flight.", metricInflight.Load())
	emit("mlsim_queue_depth", "gauge", "Last observed depth of the inference queue.", metricQueueDeep.Load())
	emit("mlsim_model_load_seconds", "gauge", "Time the model took to load.", metricModelLoad.Load())

	var readyVal int64
	if ready.Load() {
		readyVal = 1
	}
	emit("mlsim_ready", "gauge", "1 when the model is loaded and serving.", readyVal)
}
