package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// The load generator drives the gateway and reports latency percentiles.
//
// Its last line of output is the machine-readable contract that every
// scenario's verify.sh parses:
//
//	RESULT requests=1200 errors=0 timeouts=0 p50_ms=812 p95_ms=1210 p99_ms=1490 ...
//
// Percentiles, not averages: a backlog shows up in the tail long before it
// shows up in the mean.

const tickInterval = 100 * time.Millisecond

type results struct {
	// recording gates measurement. Traffic during the warm-up window is sent
	// but not recorded, so an autoscaler's convergence time does not pollute
	// the percentiles a scenario is judged on.
	recording atomic.Bool

	mu        sync.Mutex
	latencies []time.Duration
	sent      int
	errors    int
	timeouts  int
}

func (r *results) record(d time.Duration, status int, err error) {
	if !r.recording.Load() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent++
	switch {
	case err != nil:
		r.errors++
	case status == http.StatusGatewayTimeout:
		r.timeouts++
		r.errors++
		r.latencies = append(r.latencies, d)
	case status >= 400:
		r.errors++
		r.latencies = append(r.latencies, d)
	default:
		r.latencies = append(r.latencies, d)
	}
}

func (r *results) percentile(p float64) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.latencies) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), r.latencies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(p / 100 * float64(len(sorted)))
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func (r *results) snapshot() (sent, errs, timeouts int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sent, r.errors, r.timeouts
}

func runLoadgen(cfg config) {
	// Probes still work in loadgen mode, so it can run as a Deployment when a
	// scenario wants continuous background traffic rather than a one-off Job.
	serveHTTP(cfg.Listen, nil)
	ready.Store(true)

	log.Printf("load: %s profile, %.1f rps, warmup %s + measure %s, against %s",
		cfg.Profile, cfg.RPS, cfg.Warmup, cfg.Duration, cfg.TargetURL)

	client := &http.Client{Timeout: cfg.RequestTimeout + 10*time.Second}
	res := &results{}
	res.recording.Store(cfg.Warmup == 0)
	var wg sync.WaitGroup

	start := time.Now()
	measureFrom := start.Add(cfg.Warmup)
	deadline := measureFrom.Add(cfg.Duration)
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	progress := time.NewTicker(10 * time.Second)
	defer progress.Stop()

	// Fractional request-per-tick rates accumulate rather than rounding away,
	// so a 0.5 rps profile still sends one request every two seconds.
	credit := 0.0

loop:
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			if now.After(deadline) {
				break loop
			}
			if !res.recording.Load() && !now.Before(measureFrom) {
				res.recording.Store(true)
				log.Printf("warmup complete — measuring for %s", cfg.Duration)
			}
			credit += rateAt(cfg, now.Sub(start)) * tickInterval.Seconds()
			for credit >= 1 {
				credit--
				wg.Add(1)
				go func() {
					defer wg.Done()
					send(client, cfg.TargetURL, res)
				}()
			}
		case <-progress.C:
			sent, errs, timeouts := res.snapshot()
			phase := "measure"
			if !res.recording.Load() {
				phase = "warmup "
			}
			log.Printf("  %s t=%3.0fs sent=%d errors=%d timeouts=%d p99=%dms",
				phase, time.Since(start).Seconds(), sent, errs, timeouts,
				res.percentile(99).Milliseconds())
		}
	}

	log.Printf("load finished, draining in-flight requests...")
	wg.Wait()

	sent, errs, timeouts := res.snapshot()
	elapsed := time.Since(measureFrom).Seconds()
	fmt.Printf("RESULT requests=%d errors=%d timeouts=%d p50_ms=%d p95_ms=%d p99_ms=%d max_ms=%d rps=%.2f duration_s=%.0f\n",
		sent, errs, timeouts,
		res.percentile(50).Milliseconds(),
		res.percentile(95).Milliseconds(),
		res.percentile(99).Milliseconds(),
		res.percentile(100).Milliseconds(),
		float64(sent)/elapsed, elapsed)
	os.Stdout.Sync()
}

// rateAt returns the requested rate at a point in the run.
func rateAt(cfg config, elapsed time.Duration) float64 {
	switch cfg.Profile {
	case "burst":
		// Alternate full rate and silence, which is what makes an
		// aggressively-tuned autoscaler thrash.
		if int(elapsed/cfg.BurstPeriod)%2 == 0 {
			return cfg.RPS
		}
		return 0
	case "ramp":
		frac := elapsed.Seconds() / (cfg.Warmup + cfg.Duration).Seconds()
		if frac > 1 {
			frac = 1
		}
		return cfg.RPS * frac
	default: // steady
		return cfg.RPS
	}
}

func send(client *http.Client, url string, res *results) {
	start := time.Now()
	resp, err := client.Get(url)
	if err != nil {
		res.record(time.Since(start), 0, err)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	res.record(time.Since(start), resp.StatusCode, nil)
}
