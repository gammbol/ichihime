//go:build spec

package postgresdb

import (
	"context"
	"testing"

	"github.com/gammbol/ichihime/internal/storage"
	"github.com/shopspring/decimal"
)

func TestSpecTransferRejectsSameAccount(t *testing.T) {
	p := newTestPostgres(t)

	account := mustInsertAccount(t, p, decimal.RequireFromString("100.00"))

	_, err := p.Transfer(storage.TransferForm{
		Source:      int(account),
		Destination: int(account),
		Amount:      decimal.RequireFromString("10.00"),
	})
	if err == nil {
		t.Fatal("same-account transfer must be rejected")
	}

	if got := mustBalance(t, p, account); !got.Equal(decimal.RequireFromString("100.00")) {
		t.Fatalf("same-account transfer changed balance to %s", got)
	}

	var completed int
	if err := p.poll.QueryRow(
		context.Background(),
		`SELECT COUNT(*) FROM transfers WHERE status = 'completed'`,
	).Scan(&completed); err != nil {
		t.Fatalf("count completed transfers: %v", err)
	}
	if completed != 0 {
		t.Fatalf("same-account request produced %d completed transfer(s)", completed)
	}
}

func TestSpecTransferRejectsInsufficientFunds(t *testing.T) {
	p := newTestPostgres(t)

	source := mustInsertAccount(t, p, decimal.RequireFromString("5.00"))
	dest := mustInsertAccount(t, p, decimal.Zero)

	_, err := p.Transfer(storage.TransferForm{
		Source:      int(source),
		Destination: int(dest),
		Amount:      decimal.RequireFromString("10.00"),
	})
	if err == nil {
		t.Fatal("transfer larger than source balance must be rejected")
	}

	if got := mustBalance(t, p, source); !got.Equal(decimal.RequireFromString("5.00")) {
		t.Fatalf("source balance=%s, want 5.00", got)
	}
	if got := mustBalance(t, p, dest); !got.Equal(decimal.Zero) {
		t.Fatalf("destination balance=%s, want 0", got)
	}

	var completed int
	if err := p.poll.QueryRow(
		context.Background(),
		`SELECT COUNT(*) FROM transfers WHERE status = 'completed'`,
	).Scan(&completed); err != nil {
		t.Fatalf("count completed transfers: %v", err)
	}
	if completed != 0 {
		t.Fatalf("insufficient-funds request produced %d completed transfer(s)", completed)
	}
}
