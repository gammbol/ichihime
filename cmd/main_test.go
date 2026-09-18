package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/gammbol/ichihime/internal/router"
	"github.com/gammbol/ichihime/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type fakeDB struct {
	mu sync.Mutex

	accounts     []storage.Account
	accountsErr  error
	account      storage.Account
	accountErr   error
	transfers    []storage.Transfer
	transfersErr error
	transfer     storage.Transfer
	transferErr  error

	gotForm       storage.TransferForm
	transferCalls int
}

func (f *fakeDB) Close() {}

func (f *fakeDB) GetAllAccounts() ([]storage.Account, error) {
	return f.accounts, f.accountsErr
}

func (f *fakeDB) GetAllTransfers() ([]storage.Transfer, error) {
	return f.transfers, f.transfersErr
}

func (f *fakeDB) GetAccountById(int) (storage.Account, error) {
	return f.account, f.accountErr
}

func (f *fakeDB) GetTransferById(int) (storage.Transfer, error) {
	return f.transfer, f.transferErr
}

func (f *fakeDB) Transfer(form storage.TransferForm) (storage.Transfer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.transferCalls++
	f.gotForm = form
	return f.transfer, f.transferErr
}

func (f *fakeDB) transferCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.transferCalls
}

func (f *fakeDB) lastTransferForm() storage.TransferForm {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gotForm
}

var errFakeCacheMiss = errors.New("idempotency key not found")

type fakeUUIDCache struct {
	mu sync.Mutex

	statuses map[string]string
	getErr   error
	setErr   error

	getCalls int
	setCalls int

	// beforeGetReturn is used by concurrency tests to force several callers
	// to observe the same cache state before any of them can call Set.
	beforeGetReturn func()
}

func newFakeUUIDCache() *fakeUUIDCache {
	return &fakeUUIDCache{statuses: make(map[string]string)}
}

func (f *fakeUUIDCache) Close() {}

func (f *fakeUUIDCache) Get(id *storage.Idempotency) error {
	f.mu.Lock()
	f.getCalls++
	getErr := f.getErr
	status, ok := f.statuses[id.Key]
	beforeReturn := f.beforeGetReturn
	f.mu.Unlock()

	if beforeReturn != nil {
		beforeReturn()
	}
	if getErr != nil {
		return getErr
	}
	if !ok {
		return errFakeCacheMiss
	}

	id.Status = status
	return nil
}

func (f *fakeUUIDCache) Set(id storage.Idempotency) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.setCalls++
	if f.setErr != nil {
		return f.setErr
	}
	f.statuses[id.Key] = id.Status
	return nil
}

func (f *fakeUUIDCache) status(key string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	status, ok := f.statuses[key]
	return status, ok
}

func (f *fakeUUIDCache) setStatus(key, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses[key] = status
}

func (f *fakeUUIDCache) setCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.setCalls
}

func newHTTPTestApp(db *fakeDB) *Application {
	return newHTTPTestAppWithCache(db, newFakeUUIDCache())
}

func newHTTPTestAppWithCache(db *fakeDB, cache *fakeUUIDCache) *Application {
	gin.SetMode(gin.TestMode)
	return NewApplication(db, cache, router.New(":0"))
}

func TestAccountsRoute(t *testing.T) {
	db := &fakeDB{
		accounts: []storage.Account{
			{ID: "1", Currency: "USD", Balance: decimal.RequireFromString("10.00")},
			{ID: "2", Currency: "USD", Balance: decimal.RequireFromString("20.00")},
		},
	}
	app := newHTTPTestApp(db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/accounts", nil)
	app.routerApp.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"id": "1"`) || !strings.Contains(rec.Body.String(), `"id": "2"`) {
		t.Fatalf("response does not contain expected accounts: %s", rec.Body.String())
	}
}

func TestAccountRouteRejectsNonNumericID(t *testing.T) {
	app := newHTTPTestApp(&fakeDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/accounts/not-a-number", nil)
	app.routerApp.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAccountRouteReturns404FromDB(t *testing.T) {
	app := newHTTPTestApp(&fakeDB{
		accountErr: errors.New("account not found"),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/accounts/999", nil)
	app.routerApp.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestPayRouteHappyPath(t *testing.T) {
	db := &fakeDB{
		transfer: storage.Transfer{
			ID:        "42",
			Source_ID: "1",
			Dest_ID:   "2",
			Amount:    decimal.RequireFromString("12.34"),
			Currency:  "USD",
			Status:    "completed",
		},
	}
	app := newHTTPTestApp(db)

	form := url.Values{
		"source": {"1"},
		"dest":   {"2"},
		"amount": {"12.34"},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pay", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Idempotency-Key", "happy-path-key")
	app.routerApp.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if db.transferCallCount() != 1 {
		t.Fatalf("Transfer called %d times, want 1", db.transferCallCount())
	}
	gotForm := db.lastTransferForm()
	if gotForm.Source != 1 || gotForm.Destination != 2 || !gotForm.Amount.Equal(decimal.RequireFromString("12.34")) {
		t.Fatalf("Transfer called with form=%+v; want source=1 destination=2 amount=12.34", gotForm)
	}
	if !strings.Contains(rec.Body.String(), `"status": "completed"`) {
		t.Fatalf("response does not contain completed transfer: %s", rec.Body.String())
	}
}

func TestPayRouteRejectsMalformedInputBeforeDB(t *testing.T) {
	tests := []struct {
		name string
		form url.Values
	}{
		{
			name: "missing source",
			form: url.Values{"dest": {"2"}, "amount": {"1.00"}},
		},
		{
			name: "non numeric source",
			form: url.Values{"source": {"abc"}, "dest": {"2"}, "amount": {"1.00"}},
		},
		{
			name: "missing destination",
			form: url.Values{"source": {"1"}, "amount": {"1.00"}},
		},
		{
			name: "invalid amount",
			form: url.Values{"source": {"1"}, "dest": {"2"}, "amount": {"not-money"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &fakeDB{}
			app := newHTTPTestApp(db)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/pay", strings.NewReader(tt.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			app.routerApp.Router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if db.transferCallCount() != 0 {
				t.Fatalf("malformed HTTP input reached DB.Transfer %d time(s); want 0", db.transferCallCount())
			}
		})
	}
}
