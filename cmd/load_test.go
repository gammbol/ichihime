package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type apiLoadResult struct {
	ok              atomic.Int64
	conflict        atomic.Int64
	otherStatus     atomic.Int64
	transportErrors atomic.Int64
	firstErrorOnce  sync.Once
	firstError      error
	latencies       []time.Duration
}

func loadTestInt(tb testing.TB, name string, fallback int) int {
	tb.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		tb.Fatalf("%s must be a positive integer, got %q", name, raw)
	}
	return value
}

func newLoadHTTPClient(workers int) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        workers,
			MaxIdleConnsPerHost: workers,
			MaxConnsPerHost:     workers,
			DisableKeepAlives:   os.Getenv("ICHIHIME_API_DISABLE_KEEPALIVE") == "1",
		},
	}
}

func runAPILoad(
	client *http.Client,
	serverURL string,
	operations int,
	workers int,
	keyForOperation func(int) string,
) (*apiLoadResult, time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	jobs := make(chan int, operations)
	for i := 0; i < operations; i++ {
		jobs <- i
	}
	close(jobs)

	result := &apiLoadResult{latencies: make([]time.Duration, operations)}
	var wg sync.WaitGroup
	started := time.Now()
	form := validPayForm().Encode()

	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for operation := range jobs {
				requestStarted := time.Now()
				req, err := http.NewRequestWithContext(
					ctx,
					http.MethodPost,
					serverURL+"/pay",
					strings.NewReader(form),
				)
				if err != nil {
					result.latencies[operation] = time.Since(requestStarted)
					result.transportErrors.Add(1)
					result.firstErrorOnce.Do(func() { result.firstError = err })
					continue
				}
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("Idempotency-Key", keyForOperation(operation))

				response, err := client.Do(req)
				result.latencies[operation] = time.Since(requestStarted)
				if err != nil {
					result.transportErrors.Add(1)
					result.firstErrorOnce.Do(func() { result.firstError = err })
					continue
				}
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()

				switch response.StatusCode {
				case http.StatusOK:
					result.ok.Add(1)
				case http.StatusConflict:
					result.conflict.Add(1)
				default:
					result.otherStatus.Add(1)
				}
			}
		}()
	}

	wg.Wait()
	return result, time.Since(started)
}

func latencyPercentiles(latencies []time.Duration) (p50, p95, p99 time.Duration) {
	ordered := append([]time.Duration(nil), latencies...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	percentile := func(percent int) time.Duration {
		index := (len(ordered)*percent + 99) / 100
		if index == 0 {
			return 0
		}
		return ordered[index-1]
	}
	return percentile(50), percentile(95), percentile(99)
}

func TestPayRouteUniqueKeysLoad(t *testing.T) {
	if os.Getenv("ICHIHIME_API_LOAD_TEST") != "1" {
		t.Skip("set ICHIHIME_API_LOAD_TEST=1 to run the HTTP API load test")
	}

	operations := loadTestInt(t, "ICHIHIME_API_LOAD_OPERATIONS", 10000)
	defaultWorkers := runtime.GOMAXPROCS(0) * 32
	if defaultWorkers > 256 {
		defaultWorkers = 256
	}
	workers := loadTestInt(t, "ICHIHIME_API_LOAD_WORKERS", defaultWorkers)

	gin.DefaultWriter = io.Discard
	db := &fakeDB{transfer: completedFakeTransfer()}
	cache := newFakeUUIDCache()
	app := newHTTPTestAppWithCache(db, cache)
	server := httptest.NewServer(app.routerApp.Router)
	defer server.Close()

	client := newLoadHTTPClient(workers)
	defer client.CloseIdleConnections()

	result, elapsed := runAPILoad(
		client,
		server.URL,
		operations,
		workers,
		func(operation int) string { return fmt.Sprintf("load-key-%d", operation) },
	)

	if result.transportErrors.Load() != 0 {
		t.Fatalf("transport errors=%d, want 0; first error=%v", result.transportErrors.Load(), result.firstError)
	}
	if result.ok.Load() != int64(operations) || result.conflict.Load() != 0 || result.otherStatus.Load() != 0 {
		t.Fatalf("responses: ok=%d conflict=%d other=%d; want %d/0/0",
			result.ok.Load(), result.conflict.Load(), result.otherStatus.Load(), operations)
	}
	if db.transferCallCount() != operations {
		t.Fatalf("DB.Transfer calls=%d, want %d", db.transferCallCount(), operations)
	}

	rps := float64(operations) / elapsed.Seconds()
	p50, p95, p99 := latencyPercentiles(result.latencies)
	t.Logf("unique-key API load: %d requests, %d workers, %s, %.0f requests/s; latency p50=%s p95=%s p99=%s",
		operations, workers, elapsed.Round(time.Millisecond), rps,
		p50.Round(time.Microsecond), p95.Round(time.Microsecond), p99.Round(time.Microsecond))
}

func TestPayRouteSameKeyLoad(t *testing.T) {
	if os.Getenv("ICHIHIME_IDEMPOTENCY_LOAD_TEST") != "1" {
		t.Skip("set ICHIHIME_IDEMPOTENCY_LOAD_TEST=1 to run the duplicate-key storm")
	}

	operations := loadTestInt(t, "ICHIHIME_API_LOAD_OPERATIONS", 10000)
	workers := loadTestInt(t, "ICHIHIME_API_LOAD_WORKERS", 256)
	if workers > operations {
		workers = operations
	}

	gin.DefaultWriter = io.Discard
	db := &fakeDB{transfer: completedFakeTransfer()}
	cache := newFakeUUIDCache()
	app := newHTTPTestAppWithCache(db, cache)

	// Make the first wave observe the key as absent at the same time. This turns
	// the GET -> SET race from a scheduler-dependent flake into a reproducible test.
	var firstWave sync.WaitGroup
	firstWave.Add(workers)
	releaseFirstWave := make(chan struct{})
	var observedGets atomic.Int64
	cache.beforeGetReturn = func() {
		if observedGets.Add(1) <= int64(workers) {
			firstWave.Done()
			<-releaseFirstWave
		}
	}

	server := httptest.NewServer(app.routerApp.Router)
	defer server.Close()
	client := newLoadHTTPClient(workers)
	defer client.CloseIdleConnections()

	type loadRun struct {
		result  *apiLoadResult
		elapsed time.Duration
	}
	done := make(chan loadRun, 1)
	go func() {
		result, elapsed := runAPILoad(
			client,
			server.URL,
			operations,
			workers,
			func(int) string { return "one-logical-request" },
		)
		done <- loadRun{result: result, elapsed: elapsed}
	}()

	firstWave.Wait()
	close(releaseFirstWave)
	run := <-done

	if run.result.transportErrors.Load() != 0 {
		t.Fatalf("transport errors=%d, want 0; first error=%v",
			run.result.transportErrors.Load(), run.result.firstError)
	}
	if run.result.otherStatus.Load() != 0 {
		t.Fatalf("unexpected HTTP statuses=%d, want 0", run.result.otherStatus.Load())
	}
	if got := run.result.ok.Load() + run.result.conflict.Load(); got != int64(operations) {
		t.Fatalf("accounted responses=%d, want %d", got, operations)
	}

	// This is the central idempotency invariant. It is intentionally stricter
	// than merely checking that later calls receive HTTP 409.
	if db.transferCallCount() != 1 {
		t.Fatalf("duplicate-key storm called DB.Transfer %d times, want exactly 1", db.transferCallCount())
	}
	if run.result.ok.Load() != 1 {
		t.Fatalf("successful responses=%d, want exactly 1", run.result.ok.Load())
	}

	rps := float64(operations) / run.elapsed.Seconds()
	t.Logf("same-key API load: %d requests, %d workers, %s, %.0f requests/s; ok=%d conflict=%d",
		operations, workers, run.elapsed.Round(time.Millisecond), rps,
		run.result.ok.Load(), run.result.conflict.Load())
}
