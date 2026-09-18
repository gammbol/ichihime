package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gammbol/ichihime/internal/storage"
	"github.com/shopspring/decimal"
)

func validPayForm() url.Values {
	return url.Values{
		"source": {"1"},
		"dest":   {"2"},
		"amount": {"12.34"},
	}
}

func performPayRequest(app *Application, form url.Values, idempotencyKey string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pay", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	app.routerApp.Router.ServeHTTP(rec, req)
	return rec
}

func completedFakeTransfer() storage.Transfer {
	return storage.Transfer{
		ID:        "42",
		Source_ID: "1",
		Dest_ID:   "2",
		Amount:    decimal.RequireFromString("12.34"),
		Currency:  "USD",
		Status:    "completed",
	}
}

func TestPayRouteRequiresIdempotencyKey(t *testing.T) {
	db := &fakeDB{transfer: completedFakeTransfer()}
	cache := newFakeUUIDCache()
	app := newHTTPTestAppWithCache(db, cache)

	rec := performPayRequest(app, validPayForm(), "")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if db.transferCallCount() != 0 {
		t.Fatalf("request without an idempotency key reached DB.Transfer %d time(s); want 0", db.transferCallCount())
	}
	if cache.getCallCount() != 0 {
		t.Fatalf("request without an idempotency key read cache %d time(s); want 0", cache.getCallCount())
	}
	if cache.setCallCount() != 0 {
		t.Fatalf("request without an idempotency key wrote to cache %d time(s); want 0", cache.setCallCount())
	}
}

func TestPayRouteReservesNewIdempotencyKeyBeforeTransfer(t *testing.T) {
	db := &fakeDB{transfer: completedFakeTransfer()}
	cache := newFakeUUIDCache()
	var statusDuringTransfer string
	var keyExistsDuringTransfer bool
	db.beforeTransfer = func(storage.TransferForm) {
		statusDuringTransfer, keyExistsDuringTransfer = cache.status("new-key")
	}
	app := newHTTPTestAppWithCache(db, cache)

	rec := performPayRequest(app, validPayForm(), "new-key")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if db.transferCallCount() != 1 {
		t.Fatalf("Transfer called %d times, want 1", db.transferCallCount())
	}
	if !keyExistsDuringTransfer || statusDuringTransfer != "pending" {
		t.Errorf("key at entry to DB.Transfer: exists=%v status=%q, want true/pending",
			keyExistsDuringTransfer, statusDuringTransfer)
	}
	status, ok := cache.status("new-key")
	if !ok {
		t.Fatal("new idempotency key was not stored")
	}
	if status != "completed" {
		t.Errorf("key after successful HTTP request: status=%q, want completed", status)
	}
	var transfer storage.Transfer
	if err := json.Unmarshal(rec.Body.Bytes(), &transfer); err != nil {
		t.Fatalf("decode successful transfer: %v; body=%s", err, rec.Body.String())
	}
	if transfer.ID != "42" || transfer.Status != "completed" {
		t.Errorf("returned transfer id=%q status=%q, want 42/completed", transfer.ID, transfer.Status)
	}
}

func TestPayRouteMarksKeyFailedAfterTransferFailure(t *testing.T) {
	db := &fakeDB{transferErr: errors.New("transfer failed")}
	cache := newFakeUUIDCache()
	var statusDuringTransfer string
	db.beforeTransfer = func(storage.TransferForm) {
		statusDuringTransfer, _ = cache.status("failed-key")
	}
	app := newHTTPTestAppWithCache(db, cache)

	rec := performPayRequest(app, validPayForm(), "failed-key")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status=%d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	if db.transferCallCount() != 1 {
		t.Fatalf("Transfer called %d times, want 1", db.transferCallCount())
	}
	if statusDuringTransfer != "pending" {
		t.Errorf("key at entry to DB.Transfer: status=%q, want pending", statusDuringTransfer)
	}
	status, ok := cache.status("failed-key")
	if !ok || status != "failed" {
		t.Errorf("key after failed transfer: exists=%v status=%q, want true/failed", ok, status)
	}

	// Current /pay contract: a failed key is terminal and rejects a replay.
	repeated := performPayRequest(app, validPayForm(), "failed-key")
	if repeated.Code != http.StatusBadRequest {
		t.Errorf("replay of failed key: status=%d, want 400; body=%s", repeated.Code, repeated.Body.String())
	}
	if db.transferCallCount() != 1 {
		t.Errorf("replay of failed key invoked DB.Transfer again: calls=%d, want 1", db.transferCallCount())
	}
}

func TestPayRouteDoesNotTreatCacheOutageAsMissingKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"connection failure", errors.New("cache unavailable")},
		{"read timeout", context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &fakeDB{transfer: completedFakeTransfer()}
			cache := newFakeUUIDCache()
			// A read failure says nothing about whether the key already exists.
			cache.setStatus("outage-key", "completed")
			cache.getErr = tc.err
			app := newHTTPTestAppWithCache(db, cache)

			rec := performPayRequest(app, validPayForm(), "outage-key")
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("status=%d, want 500; body=%s", rec.Code, rec.Body.String())
			}
			if db.transferCallCount() != 0 {
				t.Errorf("cache read error reached DB.Transfer %d time(s); want 0", db.transferCallCount())
			}
			if cache.setCallCount() != 0 {
				t.Errorf("cache read error caused %d writes; want 0", cache.setCallCount())
			}
		})
	}
}

func TestPayRouteHandlesKnownIdempotencyStatuses(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		wantStatus int
	}{
		{name: "pending", status: "pending", wantStatus: http.StatusConflict},
		{name: "completed", status: "completed", wantStatus: http.StatusConflict},
		{name: "failed", status: "failed", wantStatus: http.StatusBadRequest},
		{name: "unknown", status: "corrupted", wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &fakeDB{transfer: completedFakeTransfer()}
			cache := newFakeUUIDCache()
			cache.setStatus("existing-key", tt.status)
			app := newHTTPTestAppWithCache(db, cache)

			rec := performPayRequest(app, validPayForm(), "existing-key")

			if rec.Code != tt.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if db.transferCallCount() != 0 {
				t.Fatalf("existing %q key reached DB.Transfer %d time(s); want 0", tt.status, db.transferCallCount())
			}
			if cache.setCallCount() != 0 {
				t.Errorf("existing key was overwritten %d time(s); want 0", cache.setCallCount())
			}
		})
	}
}

func TestPayRouteDoesNotTransferWhenKeyReservationFails(t *testing.T) {
	db := &fakeDB{transfer: completedFakeTransfer()}
	cache := newFakeUUIDCache()
	cache.setErr = errors.New("cache write failed")
	app := newHTTPTestAppWithCache(db, cache)

	rec := performPayRequest(app, validPayForm(), "new-key")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	if db.transferCallCount() != 0 {
		t.Fatalf("failed key reservation reached DB.Transfer %d time(s); want 0", db.transferCallCount())
	}
	if cache.setCallCount() != 1 {
		t.Errorf("reservation writes=%d, want 1; ensure the request reached the reservation step", cache.setCallCount())
	}
	if status, exists := cache.status("new-key"); exists {
		t.Errorf("failed reservation stored key with status %q", status)
	}
}

func TestPayRouteSequentialDuplicateKeyTransfersOnlyOnce(t *testing.T) {
	db := &fakeDB{transfer: completedFakeTransfer()}
	cache := newFakeUUIDCache()
	app := newHTTPTestAppWithCache(db, cache)

	first := performPayRequest(app, validPayForm(), "duplicate-key")
	second := performPayRequest(app, validPayForm(), "duplicate-key")

	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d, want 200; body=%s", first.Code, first.Body.String())
	}
	if second.Code != http.StatusConflict {
		t.Fatalf("duplicate status=%d, want 409; body=%s", second.Code, second.Body.String())
	}
	if db.transferCallCount() != 1 {
		t.Fatalf("same key called DB.Transfer %d times, want 1", db.transferCallCount())
	}
}

func TestPayRouteSameKeyDifferentPayloadDoesNotCreateSecondTransfer(t *testing.T) {
	db := &fakeDB{transfer: completedFakeTransfer()}
	cache := newFakeUUIDCache()
	app := newHTTPTestAppWithCache(db, cache)

	first := performPayRequest(app, validPayForm(), "reused-key")
	secondForm := validPayForm()
	secondForm.Set("amount", "99.99")
	second := performPayRequest(app, secondForm, "reused-key")

	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d, want 200; body=%s", first.Code, first.Body.String())
	}
	if second.Code != http.StatusConflict {
		t.Fatalf("same key with a different payload status=%d, want 409; body=%s", second.Code, second.Body.String())
	}
	if db.transferCallCount() != 1 {
		t.Fatalf("same key with different payload called DB.Transfer %d times, want 1", db.transferCallCount())
	}
}
