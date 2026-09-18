//go:build spec

package main

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

// Lifecycle and cache error regressions now run in the default suite.
// This specification still exposes the non-atomic Redis GET -> SET flow.

func TestSpecPayRouteConcurrentSameKeyTransfersOnlyOnce(t *testing.T) {
	const requests = 32

	db := &fakeDB{transfer: completedFakeTransfer()}
	cache := newFakeUUIDCache()
	app := newHTTPTestAppWithCache(db, cache)

	var allGets sync.WaitGroup
	allGets.Add(requests)
	releaseGets := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseGets) }) }
	defer release()
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

	allGetsDone := make(chan struct{})
	go func() {
		allGets.Wait()
		close(allGetsDone)
	}()
	select {
	case <-allGetsDone:
	case <-time.After(10 * time.Second):
		release()
		wg.Wait()
		t.Fatal("requests did not all reach cache lookup within 10s")
	}
	release()
	wg.Wait()
	close(statuses)

	successful := 0
	for status := range statuses {
		if status == http.StatusOK {
			successful++
		}
	}

	if db.transferCallCount() != 1 {
		t.Fatalf("%d simultaneous requests with one key called DB.Transfer %d times, want exactly 1",
			requests, db.transferCallCount())
	}
	if successful != 1 {
		t.Fatalf("successful HTTP responses=%d, want exactly 1", successful)
	}
}
