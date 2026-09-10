//go:build spec

package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSpecPayRouteRejectsMalformedInputWith400(t *testing.T) {
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
			app := newHTTPTestApp(&fakeDB{})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/pay", strings.NewReader(tt.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			app.routerApp.Router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}
