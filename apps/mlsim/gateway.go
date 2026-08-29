package main

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// The gateway accepts inference requests and hands them to workers through a
// Redis list, then blocks on a per-request reply key.
//
// This shape is the whole point of the first module: the gateway is almost
// pure wait, so its CPU stays near idle no matter how deep the backlog gets.
// A CPU-target HPA cannot see the thing that is actually going wrong.

var requestSeq atomic.Int64

func runGateway(cfg config) {
	pool := newRedisPool(cfg.RedisAddr)

	serveHTTP(cfg.Listen, func(mux *http.ServeMux) {
		mux.HandleFunc("/infer", func(w http.ResponseWriter, r *http.Request) {
			handleInfer(w, r, cfg, pool)
		})
	})

	go pollQueueDepth(cfg, pool)

	ready.Store(true)
	log.Printf("gateway ready — queue=%s redis=%s timeout=%s",
		cfg.Queue, cfg.RedisAddr, cfg.RequestTimeout)
	select {}
}

func handleInfer(w http.ResponseWriter, _ *http.Request, cfg config, pool *redisPool) {
	start := time.Now()
	metricRequests.Add(1)
	metricInflight.Add(1)
	defer metricInflight.Add(-1)

	id := strconv.FormatInt(requestSeq.Add(1), 10) + "-" + strconv.FormatInt(start.UnixNano(), 10)

	if err := pool.LPush(cfg.Queue, id); err != nil {
		metricErrors.Add(1)
		http.Error(w, "enqueue failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Block until a worker answers. Every second spent here is queue wait plus
	// inference time, which is exactly the latency a user experiences.
	reply, err := pool.BPop("BLPOP", "reply:"+id, cfg.RequestTimeout)
	elapsed := time.Since(start)

	switch {
	case err != nil:
		metricErrors.Add(1)
		http.Error(w, "reply wait failed: "+err.Error(), http.StatusBadGateway)
	case reply == "":
		metricTimeouts.Add(1)
		metricErrors.Add(1)
		http.Error(w, "timed out waiting for a worker", http.StatusGatewayTimeout)
	case reply == "error":
		metricErrors.Add(1)
		http.Error(w, "inference failed", http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok latency_ms=%d\n", elapsed.Milliseconds())
	}
}

// pollQueueDepth keeps mlsim_queue_depth fresh so the backlog is visible in
// /metrics as well as in redis-cli.
func pollQueueDepth(cfg config, pool *redisPool) {
	for {
		if n, err := pool.LLen(cfg.Queue); err == nil {
			metricQueueDeep.Store(n)
		}
		time.Sleep(5 * time.Second)
	}
}
