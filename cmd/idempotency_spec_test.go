//go:build spec

package main

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
)

// These tests describe the guarantees the idempotency layer must eventually
// provide. They intentionally expose gaps in the current Redis GET -> SET flow.

func TestSpecPayRouteMarksKeyCompletedAfterSuccess(t *testing.T) {
	db := &fakeDB{transfer: completedFakeTransfer()}
	cache := newFakeUUIDCache()
	app := newHTTPTestAppWithCache(db, cache)

	rec := performPayRequest(app, validPayForm(), "successful-key")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	status, ok := cache.status("successful-key")
	if !ok {
		t.Fatal("successful idempotency key was not stored")
	}
	if status != "completed" {
		t.Fatalf("successful key status=%q, want completed", status)
	}
}

func TestSpecPayRouteMarksKeyFailedAfterTransferFailure(t *testing.T) {
	db := &fakeDB{transferErr: errors.New("transfer failed")}
	cache := newFakeUUIDCache()
	app := newHTTPTestAppWithCache(db, cache)

	rec := performPayRequest(app, validPayForm(), "failed-key")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	status, ok := cache.status("failed-key")
	if !ok {
		t.Fatal("failed idempotency key was not stored")
	}
	if status != "failed" {
		t.Fatalf("failed key status=%q, want failed", status)
	}
}

func TestSpecPayRouteDoesNotTreatCacheOutageAsMissingKey(t *testing.T) {
	db := &fakeDB{transfer: completedFakeTransfer()}
	cache := newFakeUUIDCache()
	cache.getErr = errors.New("cache unavailable")
	app := newHTTPTestAppWithCache(db, cache)

	rec := performPayRequest(app, validPayForm(), "outage-key")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	if db.transferCallCount() != 0 {
		t.Fatalf("cache outage reached DB.Transfer %d time(s); want 0", db.transferCallCount())
	}
}

func TestSpecPayRouteConcurrentSameKeyTransfersOnlyOnce(t *testing.T) {
	const requests = 32

	db := &fakeDB{transfer: completedFakeTransfer()}
	cache := newFakeUUIDCache()
	app := newHTTPTestAppWithCache(db, cache)

	var allGets sync.WaitGroup
	allGets.Add(requests)
	releaseGets := make(chan struct{})
	cache.beforeGetReturn = func() {
		allGets.Done()
		<-releaseGets
	}

	statuses := make(chan int, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := performPayRequest(app, validPayForm(), "one-logical-request")
			statuses <- rec.Code
		}()
	}

	allGets.Wait()
	close(releaseGets)
	wg.Wait()
	close(statuses)

	var successful atomic.Int64
	for status := range statuses {
		if status == http.StatusOK {
			successful.Add(1)
		}
	}

	if db.transferCallCount() != 1 {
		t.Fatalf("%d simultaneous requests with one key called DB.Transfer %d times, want exactly 1",
			requests, db.transferCallCount())
	}
	if successful.Load() != 1 {
		t.Fatalf("successful HTTP responses=%d, want exactly 1", successful.Load())
	}
}
