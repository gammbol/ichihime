package main

import (
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
	if cache.setCallCount() != 0 {
		t.Fatalf("request without an idempotency key wrote to cache %d time(s); want 0", cache.setCallCount())
	}
}

func TestPayRouteReservesNewIdempotencyKeyBeforeTransfer(t *testing.T) {
	db := &fakeDB{transfer: completedFakeTransfer()}
	cache := newFakeUUIDCache()
	app := newHTTPTestAppWithCache(db, cache)

	rec := performPayRequest(app, validPayForm(), "new-key")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if db.transferCallCount() != 1 {
		t.Fatalf("Transfer called %d times, want 1", db.transferCallCount())
	}
	status, ok := cache.status("new-key")
	if !ok {
		t.Fatal("new idempotency key was not stored")
	}
	if status != "pending" {
		t.Fatalf("stored status=%q, want pending", status)
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
