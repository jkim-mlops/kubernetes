// mlsim simulates the workloads of a small ML platform: an inference gateway,
// a queue-consuming model worker, and a load generator.
//
// One binary, one image. The role is chosen with MODE, and every behaviour a
// scenario needs to bend — latency, error rate, model load time, memory
// footprint — is an environment variable. That is what lets the lab inject a
// new failure by editing a manifest instead of writing new code.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type config struct {
	Mode   string
	Listen string

	RedisAddr string
	Queue     string

	// Worker behaviour
	ModelLoadSeconds float64
	MemPerModelMB    int
	MemPerRequestMB  int
	LatencyMS        int
	LatencyJitterMS  int
	CPUBurnMS        int
	ErrorRate        float64
	HangRate         float64
	HangSeconds      float64
	GPUMemMB         int

	// Gateway behaviour
	RequestTimeout time.Duration

	// Loadgen behaviour
	TargetURL   string
	RPS         float64
	Warmup      time.Duration
	Duration    time.Duration
	Profile     string
	BurstPeriod time.Duration
}

func loadConfig() config {
	return config{
		Mode:   env("MODE", "gateway"),
		Listen: env("LISTEN_ADDR", ":8080"),

		RedisAddr: env("REDIS_ADDR", "redis:6379"),
		Queue:     env("QUEUE_NAME", "infer:queue"),

		ModelLoadSeconds: envFloat("MODEL_LOAD_SECONDS", 0),
		MemPerModelMB:    envInt("MEM_PER_MODEL_MB", 0),
		MemPerRequestMB:  envInt("MEM_PER_REQUEST_MB", 0),
		LatencyMS:        envInt("LATENCY_MS", 800),
		LatencyJitterMS:  envInt("LATENCY_JITTER_MS", 100),
		CPUBurnMS:        envInt("CPU_BURN_MS", 15),
		ErrorRate:        envFloat("ERROR_RATE", 0),
		HangRate:         envFloat("HANG_RATE", 0),
		HangSeconds:      envFloat("HANG_SECONDS", 30),
		GPUMemMB:         envInt("GPU_MEM_MB", 0),

		RequestTimeout: envDuration("REQUEST_TIMEOUT", 60*time.Second),

		TargetURL:   env("TARGET_URL", "http://inference-gateway:8080/infer"),
		RPS:         envFloat("RPS", 10),
		Warmup:      envDuration("WARMUP", 0),
		Duration:    envDuration("DURATION", 60*time.Second),
		Profile:     env("PROFILE", "steady"),
		BurstPeriod: envDuration("BURST_PERIOD", 30*time.Second),
	}
}

func main() {
	log.SetFlags(log.Ltime)
	cfg := loadConfig()

	switch strings.ToLower(cfg.Mode) {
	case "gateway":
		runGateway(cfg)
	case "worker":
		runWorker(cfg)
	case "loadgen":
		runLoadgen(cfg)
	default:
		log.Fatalf("unknown MODE %q (want gateway, worker or loadgen)", cfg.Mode)
	}
}

// ------------------------------------------------------ probes & metrics ---

// ready gates /readyz. /healthz is deliberately always-200 once the process is
// up: pointing a livenessProbe at /readyz instead is a real misconfiguration
// the lab teaches, and it only bites if the two endpoints differ.
var ready atomic.Bool

// serveHTTP starts the probe/metrics listener shared by every mode. extra lets
// a mode register its own handlers on the same mux.
func serveHTTP(addr string, extra func(*http.ServeMux)) {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintln(w, "loading model")
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ready")
	})
	mux.Handle("/metrics", promhttp.Handler())

	if extra != nil {
		extra(mux)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()
	log.Printf("listening on %s", addr)
}

// --------------------------------------------------------------- memory ---

// held keeps simulated model weights alive for the process lifetime so the
// allocation shows up in the container's RSS and can trigger a real OOMKill.
// Workers append to it from several goroutines, so it needs a lock.
var (
	heldMu sync.Mutex
	held   [][]byte
)

// retain allocates and keeps mb megabytes for the life of the process.
func retain(mb int) {
	buf := allocate(mb)
	if buf == nil {
		return
	}
	heldMu.Lock()
	held = append(held, buf)
	heldMu.Unlock()
}

// allocate touches one byte per page, because Go's allocator will not fault in
// pages that are never written and the memory would not count against the
// container limit.
func allocate(mb int) []byte {
	if mb <= 0 {
		return nil
	}
	buf := make([]byte, mb<<20)
	for i := 0; i < len(buf); i += 4096 {
		buf[i] = 1
	}
	return buf
}

func burnCPU(d time.Duration) {
	if d <= 0 {
		return
	}
	deadline := time.Now().Add(d)
	x := 0
	for time.Now().Before(deadline) {
		for i := 0; i < 100000; i++ {
			x += i % 7
		}
	}
	_ = x
}

// ------------------------------------------------------------------ env ---

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(env(key, "")); err == nil {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(env(key, ""), 64); err == nil {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	raw := env(key, "")
	if raw == "" {
		return def
	}
	if v, err := time.ParseDuration(raw); err == nil {
		return v
	}
	// Bare numbers are seconds, so manifests can say "60" as well as "60s".
	if v, err := strconv.Atoi(raw); err == nil {
		return time.Duration(v) * time.Second
	}
	return def
}
