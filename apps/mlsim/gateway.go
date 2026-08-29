package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// The gateway accepts inference requests and hands them to workers through a
// Redis list, then blocks on a per-request reply key.
//
// This shape is the whole point of the first module: the gateway is almost
// pure wait, so its CPU stays near idle no matter how deep the backlog gets.
// A CPU-target HPA cannot see the thing that is actually going wrong.

var requestSeq atomic.Int64

func runGateway(cfg config) {
	rdb := newRedisClient(cfg.RedisAddr)

	serveHTTP(cfg.Listen, func(mux *http.ServeMux) {
		mux.HandleFunc("/infer", func(w http.ResponseWriter, r *http.Request) {
			handleInfer(w, r, cfg, rdb)
		})
	})

	go pollQueueDepth(cfg, rdb)

	ready.Store(true)
	log.Printf("gateway ready — queue=%s redis=%s timeout=%s",
		cfg.Queue, cfg.RedisAddr, cfg.RequestTimeout)
	select {}
}

func handleInfer(w http.ResponseWriter, _ *http.Request, cfg config, rdb *redis.Client) {
	// Deliberately not r.Context(). A gateway that abandons its wait when the
	// caller hangs up is better engineering in general, but here it would make
	// the latency and timeout numbers every verify.sh is judged on depend on
	// when a load generator happened to disconnect. The wait is bounded by
	// REQUEST_TIMEOUT and by nothing else, which is what the scenarios assume.
	ctx, cancel := context.WithTimeout(context.Background(), cfg.RequestTimeout+5*time.Second)
	defer cancel()

	start := time.Now()
	metricRequests.Inc()
	metricInflight.Inc()
	defer metricInflight.Dec()

	id := strconv.FormatInt(requestSeq.Add(1), 10) + "-" + strconv.FormatInt(start.UnixNano(), 10)

	if err := rdb.LPush(ctx, cfg.Queue, id).Err(); err != nil {
		metricErrors.Inc()
		http.Error(w, "enqueue failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Block until a worker answers. Every second spent here is queue wait plus
	// inference time, which is exactly the latency a user experiences.
	reply, err := blockingPop(ctx, rdb.BLPop, "reply:"+id, cfg.RequestTimeout)
	elapsed := time.Since(start)
	metricRequestSeconds.Observe(elapsed.Seconds())

	switch {
	case err != nil:
		metricErrors.Inc()
		http.Error(w, "reply wait failed: "+err.Error(), http.StatusBadGateway)
	case reply == "":
		metricTimeouts.Inc()
		metricErrors.Inc()
		http.Error(w, "timed out waiting for a worker", http.StatusGatewayTimeout)
	case reply == "error":
		metricErrors.Inc()
		http.Error(w, "inference failed", http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok latency_ms=%d\n", elapsed.Milliseconds())
	}
}

// pollQueueDepth keeps mlsim_queue_depth fresh so the backlog is visible in
// /metrics as well as in redis-cli.
func pollQueueDepth(cfg config, rdb *redis.Client) {
	ctx := context.Background()
	for {
		if n, err := rdb.LLen(ctx, cfg.Queue).Result(); err == nil {
			metricQueueDepth.Set(float64(n))
		}
		time.Sleep(5 * time.Second)
	}
}
