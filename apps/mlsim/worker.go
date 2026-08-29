package main

import (
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"
)

// The worker pops jobs off the queue and simulates inference: a fixed think
// time (mostly sleep, like a GPU call), a small CPU burn, and an optional
// memory footprint.
//
// Throughput is deliberately easy to reason about: one worker with
// CONCURRENCY=1 and LATENCY_MS=800 serves 1.25 requests per second. That makes
// "how many replicas do I actually need?" a calculation the reader can do in
// their head and then check.

func runWorker(cfg config) {
	serveHTTP(cfg.Listen, nil)

	// Model load happens before the worker reports ready. With
	// MODEL_LOAD_SECONDS set high this is the cold start that makes
	// scale-to-zero expensive — and the window in which a misaimed
	// livenessProbe will kill the pod before it ever serves a request.
	if cfg.ModelLoadSeconds > 0 {
		log.Printf("loading model (%.0fs, %d MB)...", cfg.ModelLoadSeconds, cfg.MemPerModelMB)
		time.Sleep(time.Duration(cfg.ModelLoadSeconds * float64(time.Second)))
	}
	if cfg.MemPerModelMB > 0 {
		retain(cfg.MemPerModelMB)
	}
	if cfg.GPUMemMB > 0 {
		log.Printf("model resident on GPU (%d MB simulated)", cfg.GPUMemMB)
	}
	metricModelLoad.Store(int64(cfg.ModelLoadSeconds))
	ready.Store(true)
	log.Printf("worker ready — queue=%s latency=%dms concurrency=%d",
		cfg.Queue, cfg.LatencyMS, envInt("CONCURRENCY", 1))

	concurrency := envInt("CONCURRENCY", 1)
	if concurrency < 1 {
		concurrency = 1
	}
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			consume(cfg)
		}()
	}
	wg.Wait()
}

func consume(cfg config) {
	pool := newRedisPool(cfg.RedisAddr)
	for {
		// A short block timeout keeps the worker responsive to shutdown and
		// lets it notice a Redis restart quickly.
		job, err := pool.BPop("BRPOP", cfg.Queue, 5*time.Second)
		if err != nil {
			log.Printf("queue read failed: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if job == "" {
			continue // idle
		}
		process(cfg, pool, job)
	}
}

func process(cfg config, pool *redisPool, job string) {
	metricRequests.Add(1)
	metricInflight.Add(1)
	defer metricInflight.Add(-1)

	id := job
	if i := strings.IndexByte(job, '|'); i >= 0 {
		id = job[:i]
	}

	// An occasional long hang is what exhausts a caller's connection pool and
	// turns one slow dependency into a cascading outage.
	if cfg.HangRate > 0 && rand.Float64() < cfg.HangRate {
		log.Printf("job %s hanging for %.0fs", id, cfg.HangSeconds)
		time.Sleep(time.Duration(cfg.HangSeconds * float64(time.Second)))
	}

	burnCPU(time.Duration(cfg.CPUBurnMS) * time.Millisecond)

	think := cfg.LatencyMS
	if cfg.LatencyJitterMS > 0 {
		think += rand.Intn(2*cfg.LatencyJitterMS+1) - cfg.LatencyJitterMS
	}
	if remaining := time.Duration(think)*time.Millisecond - time.Duration(cfg.CPUBurnMS)*time.Millisecond; remaining > 0 {
		time.Sleep(remaining)
	}

	if cfg.MemPerRequestMB > 0 {
		retain(cfg.MemPerRequestMB)
	}

	reply := "ok"
	if cfg.ErrorRate > 0 && rand.Float64() < cfg.ErrorRate {
		reply = "error"
		metricErrors.Add(1)
	}
	if err := pool.PushReply("reply:"+id, reply, 60*time.Second); err != nil {
		log.Printf("reply for %s failed: %v", id, err)
	}
}
