package postgresdb

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gammbol/ichihime/internal/storage"
	pgxdecimal "github.com/jackc/pgx-shopspring-decimal"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"
)

var testSchema = []string{
	`DROP TABLE IF EXISTS transfers`,
	`DROP TABLE IF EXISTS accounts`,
	`CREATE TABLE accounts (
		id          BIGINT PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
		currency    VARCHAR(3) NOT NULL,
		balance     NUMERIC(15,2) DEFAULT 0.00,
		created_at  TIMESTAMPTZ NOT NULL DEFAULT current_timestamp
	)`,
	`CREATE TABLE transfers (
		id          BIGINT PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
		source_id   BIGINT REFERENCES accounts NOT NULL,
		dest_id     BIGINT REFERENCES accounts NOT NULL,
		amount      NUMERIC(15,2) NOT NULL,
		currency    VARCHAR(3) NOT NULL,
		status      VARCHAR(16) NOT NULL
		            CHECK (status IN ('pending', 'completed', 'failed'))
		            DEFAULT 'pending',
		created_at  TIMESTAMPTZ NOT NULL DEFAULT current_timestamp
	)`,
}

func newTestPostgres(tb testing.TB) *Postgres {
	tb.Helper()
	dotenvErr := godotenv.Load("../../../.env")
	if dotenvErr != nil {
		tb.Fatalf("load godotenv: %v", dotenvErr)
	}

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		tb.Skip("TEST_DATABASE_URL is not set; skipping PostgreSQL integration tests")
	}

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		tb.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}

	// Concurrency tests need more than the default minimum pool capacity.
	config.MaxConns = 64
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		pgxdecimal.Register(conn.TypeMap())
		return nil
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		tb.Fatalf("create test pool: %v", err)
	}
	tb.Cleanup(pool.Close)

	if err := pool.Ping(context.Background()); err != nil {
		tb.Fatalf("ping test database: %v", err)
	}

	// These tests are destructive by design. Always use a dedicated test DB.
	for _, statement := range testSchema {
		if _, err := pool.Exec(context.Background(), statement); err != nil {
			tb.Fatalf("reset test schema: %v", err)
		}
	}

	return &Postgres{poll: pool}
}

func makeTransferForm(source, destination int64, amount decimal.Decimal) storage.TransferForm {
	return storage.TransferForm{
		Source:      int(source),
		Destination: int(destination),
		Amount:      amount,
	}
}

func mustInsertAccount(tb testing.TB, p *Postgres, balance decimal.Decimal) int64 {
	tb.Helper()

	var id int64
	err := p.poll.QueryRow(
		context.Background(),
		`INSERT INTO accounts (currency, balance)
		 VALUES ('USD', $1)
		 RETURNING id`,
		balance,
	).Scan(&id)
	if err != nil {
		tb.Fatalf("insert account: %v", err)
	}
	return id
}

func mustBalance(tb testing.TB, p *Postgres, id int64) decimal.Decimal {
	tb.Helper()

	var balance decimal.Decimal
	if err := p.poll.QueryRow(
		context.Background(),
		`SELECT balance FROM accounts WHERE id = $1`,
		id,
	).Scan(&balance); err != nil {
		tb.Fatalf("read balance for account %d: %v", id, err)
	}
	return balance
}

func mustTotalBalance(tb testing.TB, p *Postgres) decimal.Decimal {
	tb.Helper()

	var total decimal.Decimal
	if err := p.poll.QueryRow(
		context.Background(),
		`SELECT COALESCE(SUM(balance), 0) FROM accounts`,
	).Scan(&total); err != nil {
		tb.Fatalf("read total balance: %v", err)
	}
	return total
}

func mustTransferStatusCounts(tb testing.TB, p *Postgres) (pending, completed, failed int) {
	tb.Helper()

	rows, err := p.poll.Query(
		context.Background(),
		`SELECT status, COUNT(*) FROM transfers GROUP BY status`,
	)
	if err != nil {
		tb.Fatalf("read transfer statuses: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			tb.Fatalf("scan transfer status: %v", err)
		}
		switch status {
		case "pending":
			pending = count
		case "completed":
			completed = count
		case "failed":
			failed = count
		}
	}
	if err := rows.Err(); err != nil {
		tb.Fatalf("iterate transfer statuses: %v", err)
	}
	return pending, completed, failed
}

func TestValidateForm(t *testing.T) {
	tests := []struct {
		name    string
		form    storage.TransferForm
		wantErr bool
	}{
		{
			name:    "valid",
			form:    storage.TransferForm{Source: 1, Destination: 2, Amount: decimal.RequireFromString("1.00")},
			wantErr: false,
		},
		{
			name:    "zero source",
			form:    storage.TransferForm{Source: 0, Destination: 2, Amount: decimal.RequireFromString("1.00")},
			wantErr: true,
		},
		{
			name:    "negative destination",
			form:    storage.TransferForm{Source: 1, Destination: -2, Amount: decimal.RequireFromString("1.00")},
			wantErr: true,
		},
		{
			name:    "zero amount",
			form:    storage.TransferForm{Source: 1, Destination: 2, Amount: decimal.Zero},
			wantErr: true,
		},
		{
			name:    "negative amount",
			form:    storage.TransferForm{Source: 1, Destination: 2, Amount: decimal.RequireFromString("-0.01")},
			wantErr: true,
		},
		{
			name:    "same account",
			form:    storage.TransferForm{Source: 1, Destination: 1, Amount: decimal.RequireFromString("1.00")},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateForm(tt.form)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateForm(%+v) error=%v, wantErr=%v", tt.form, err, tt.wantErr)
			}
		})
	}
}

func TestReadMethods(t *testing.T) {
	p := newTestPostgres(t)

	source := mustInsertAccount(t, p, decimal.RequireFromString("100.00"))
	dest := mustInsertAccount(t, p, decimal.RequireFromString("25.00"))

	accounts, err := p.GetAllAccounts()
	if err != nil {
		t.Fatalf("GetAllAccounts: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("GetAllAccounts: got %d accounts, want 2", len(accounts))
	}

	account, err := p.GetAccountById(int(source))
	if err != nil {
		t.Fatalf("GetAccountById: %v", err)
	}
	if account.ID != strconv.FormatInt(source, 10) {
		t.Fatalf("GetAccountById: got id %q, want %d", account.ID, source)
	}
	if !account.Balance.Equal(decimal.RequireFromString("100.00")) {
		t.Fatalf("GetAccountById: got balance %s, want 100.00", account.Balance)
	}

	tr, err := p.Transfer(makeTransferForm(source, dest, decimal.RequireFromString("5.25")))
	if err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	if tr.Status != "completed" {
		t.Fatalf("Transfer: got status %q, want completed", tr.Status)
	}

	transfers, err := p.GetAllTransfers()
	if err != nil {
		t.Fatalf("GetAllTransfers: %v", err)
	}
	if len(transfers) != 1 {
		t.Fatalf("GetAllTransfers: got %d transfers, want 1", len(transfers))
	}

	transferID, err := strconv.Atoi(tr.ID)
	if err != nil {
		t.Fatalf("returned transfer id %q is not an integer: %v", tr.ID, err)
	}

	stored, err := p.GetTransferById(transferID)
	if err != nil {
		t.Fatalf("GetTransferById: %v", err)
	}
	if stored.ID != tr.ID || stored.Status != "completed" {
		t.Fatalf("GetTransferById: got id=%q status=%q; want id=%q status=completed",
			stored.ID, stored.Status, tr.ID)
	}
}

func TestTransferHappyPath(t *testing.T) {
	p := newTestPostgres(t)

	source := mustInsertAccount(t, p, decimal.RequireFromString("1000.00"))
	dest := mustInsertAccount(t, p, decimal.RequireFromString("250.00"))
	beforeTotal := mustTotalBalance(t, p)

	tr, err := p.Transfer(makeTransferForm(source, dest, decimal.RequireFromString("125.50")))
	if err != nil {
		t.Fatalf("Transfer: %v", err)
	}

	if tr.Status != "completed" {
		t.Fatalf("status = %q, want completed", tr.Status)
	}
	if tr.Source_ID != strconv.FormatInt(source, 10) {
		t.Fatalf("source_id = %q, want %d", tr.Source_ID, source)
	}
	if tr.Dest_ID != strconv.FormatInt(dest, 10) {
		t.Fatalf("dest_id = %q, want %d", tr.Dest_ID, dest)
	}
	if !tr.Amount.Equal(decimal.RequireFromString("125.50")) {
		t.Fatalf("amount = %s, want 125.50", tr.Amount)
	}

	if got := mustBalance(t, p, source); !got.Equal(decimal.RequireFromString("874.50")) {
		t.Fatalf("source balance = %s, want 874.50", got)
	}
	if got := mustBalance(t, p, dest); !got.Equal(decimal.RequireFromString("375.50")) {
		t.Fatalf("destination balance = %s, want 375.50", got)
	}
	if got := mustTotalBalance(t, p); !got.Equal(beforeTotal) {
		t.Fatalf("total balance changed: before=%s after=%s", beforeTotal, got)
	}

	pending, completed, failed := mustTransferStatusCounts(t, p)
	if pending != 0 || completed != 1 || failed != 0 {
		t.Fatalf("statuses: pending=%d completed=%d failed=%d; want 0/1/0",
			pending, completed, failed)
	}
}

func TestTransferRejectsInvalidArgumentsWithoutChangingState(t *testing.T) {
	tests := []struct {
		name   string
		source int
		dest   int
		amount decimal.Decimal
	}{
		{"zero source", 0, 2, decimal.NewFromInt(1)},
		{"negative source", -1, 2, decimal.NewFromInt(1)},
		{"zero destination", 1, 0, decimal.NewFromInt(1)},
		{"negative destination", 1, -1, decimal.NewFromInt(1)},
		{"zero amount", 1, 2, decimal.Zero},
		{"negative amount", 1, 2, decimal.RequireFromString("-0.01")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestPostgres(t)
			source := mustInsertAccount(t, p, decimal.RequireFromString("100.00"))
			dest := mustInsertAccount(t, p, decimal.RequireFromString("50.00"))

			actualSource := tt.source
			actualDest := tt.dest
			if tt.source == 1 {
				actualSource = int(source)
			}
			if tt.dest == 2 {
				actualDest = int(dest)
			}

			_, err := p.Transfer(storage.TransferForm{Source: actualSource, Destination: actualDest, Amount: tt.amount})
			if err == nil {
				t.Fatalf("Transfer(%+v): expected error", storage.TransferForm{Source: actualSource, Destination: actualDest, Amount: tt.amount})
			}

			if got := mustBalance(t, p, source); !got.Equal(decimal.RequireFromString("100.00")) {
				t.Fatalf("source balance changed to %s", got)
			}
			if got := mustBalance(t, p, dest); !got.Equal(decimal.RequireFromString("50.00")) {
				t.Fatalf("destination balance changed to %s", got)
			}

			pending, completed, failed := mustTransferStatusCounts(t, p)
			if pending+completed+failed != 0 {
				t.Fatalf("invalid request created a transfer row: pending=%d completed=%d failed=%d",
					pending, completed, failed)
			}
		})
	}
}

func TestTransferRejectsMissingAccounts(t *testing.T) {
	tests := []struct {
		name          string
		missingSource bool
		missingDest   bool
	}{
		{"missing source", true, false},
		{"missing destination", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestPostgres(t)
			source := mustInsertAccount(t, p, decimal.RequireFromString("100.00"))
			dest := mustInsertAccount(t, p, decimal.RequireFromString("50.00"))

			sourceArg := int(source)
			destArg := int(dest)
			if tt.missingSource {
				sourceArg = 999999
			}
			if tt.missingDest {
				destArg = 999999
			}

			_, err := p.Transfer(storage.TransferForm{Source: sourceArg, Destination: destArg, Amount: decimal.RequireFromString("10.00")})
			if err == nil {
				t.Fatal("expected error for missing account")
			}

			if got := mustBalance(t, p, source); !got.Equal(decimal.RequireFromString("100.00")) {
				t.Fatalf("source balance changed to %s", got)
			}
			if got := mustBalance(t, p, dest); !got.Equal(decimal.RequireFromString("50.00")) {
				t.Fatalf("destination balance changed to %s", got)
			}
		})
	}
}

func TestTransferRejectsSameAccountWithoutCreatingRow(t *testing.T) {
	p := newTestPostgres(t)
	account := mustInsertAccount(t, p, decimal.RequireFromString("100.00"))

	_, err := p.Transfer(makeTransferForm(account, account, decimal.RequireFromString("10.00")))
	if err == nil {
		t.Fatal("same-account transfer must be rejected")
	}

	if got := mustBalance(t, p, account); !got.Equal(decimal.RequireFromString("100.00")) {
		t.Fatalf("same-account transfer changed balance to %s", got)
	}

	pending, completed, failed := mustTransferStatusCounts(t, p)
	if pending+completed+failed != 0 {
		t.Fatalf("validation failure must not create a transfer row: pending=%d completed=%d failed=%d", pending, completed, failed)
	}
}

func TestTransferRejectsInsufficientFundsAndRollsBack(t *testing.T) {
	p := newTestPostgres(t)
	source := mustInsertAccount(t, p, decimal.RequireFromString("5.00"))
	dest := mustInsertAccount(t, p, decimal.Zero)

	_, err := p.Transfer(makeTransferForm(source, dest, decimal.RequireFromString("10.00")))
	if err == nil {
		t.Fatal("transfer larger than source balance must be rejected")
	}

	if got := mustBalance(t, p, source); !got.Equal(decimal.RequireFromString("5.00")) {
		t.Fatalf("source balance=%s, want 5.00", got)
	}
	if got := mustBalance(t, p, dest); !got.Equal(decimal.Zero) {
		t.Fatalf("destination balance=%s, want 0", got)
	}

	pending, completed, failed := mustTransferStatusCounts(t, p)
	if pending != 0 || completed != 0 || failed != 1 {
		t.Fatalf("statuses: pending=%d completed=%d failed=%d; want 0/0/1", pending, completed, failed)
	}
}

func TestTransferAllowsSpendingEntireBalance(t *testing.T) {
	p := newTestPostgres(t)
	source := mustInsertAccount(t, p, decimal.RequireFromString("100.00"))
	dest := mustInsertAccount(t, p, decimal.RequireFromString("20.00"))
	beforeTotal := mustTotalBalance(t, p)

	_, err := p.Transfer(makeTransferForm(source, dest, decimal.RequireFromString("100.00")))
	if err != nil {
		t.Fatalf("an account with exactly enough funds must be allowed to transfer them all: %v", err)
	}

	if got := mustBalance(t, p, source); !got.Equal(decimal.Zero) {
		t.Fatalf("source balance=%s, want 0", got)
	}
	if got := mustBalance(t, p, dest); !got.Equal(decimal.RequireFromString("120.00")) {
		t.Fatalf("destination balance=%s, want 120.00", got)
	}
	if got := mustTotalBalance(t, p); !got.Equal(beforeTotal) {
		t.Fatalf("total balance changed: before=%s after=%s", beforeTotal, got)
	}
}

func TestTransferAllowsAffordableAmountGreaterThanHalfBalance(t *testing.T) {
	p := newTestPostgres(t)
	source := mustInsertAccount(t, p, decimal.RequireFromString("100.00"))
	dest := mustInsertAccount(t, p, decimal.Zero)

	_, err := p.Transfer(makeTransferForm(source, dest, decimal.RequireFromString("60.00")))
	if err != nil {
		t.Fatalf("transfer must succeed because 60.00 <= 100.00: %v", err)
	}

	if got := mustBalance(t, p, source); !got.Equal(decimal.RequireFromString("40.00")) {
		t.Fatalf("source balance=%s, want 40.00", got)
	}
	if got := mustBalance(t, p, dest); !got.Equal(decimal.RequireFromString("60.00")) {
		t.Fatalf("destination balance=%s, want 60.00", got)
	}
}

func TestTransferIsAtomicWhenDestinationUpdateFails(t *testing.T) {
	p := newTestPostgres(t)

	source := mustInsertAccount(t, p, decimal.RequireFromString("100.00"))
	dest := mustInsertAccount(t, p, decimal.RequireFromString("50.00"))
	beforeTotal := mustTotalBalance(t, p)

	// Inject a database failure specifically on the destination account update.
	fn := fmt.Sprintf(`
CREATE OR REPLACE FUNCTION ichihime_test_fail_destination()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
	IF NEW.id = %d THEN
		RAISE EXCEPTION 'injected destination update failure';
	END IF;
	RETURN NEW;
END;
$$;
`, dest)
	if _, err := p.poll.Exec(context.Background(), fn); err != nil {
		t.Fatalf("create failure-injection function: %v", err)
	}
	if _, err := p.poll.Exec(
		context.Background(),
		`CREATE TRIGGER ichihime_test_fail_destination_trigger
		 BEFORE UPDATE ON accounts
		 FOR EACH ROW
		 EXECUTE FUNCTION ichihime_test_fail_destination()`,
	); err != nil {
		t.Fatalf("create failure-injection trigger: %v", err)
	}

	_, err := p.Transfer(makeTransferForm(source, dest, decimal.RequireFromString("25.00")))
	if err == nil {
		t.Fatal("expected transfer to fail")
	}

	// The debit happened first in the transaction. If rollback is correct,
	// neither account may show a partial update.
	if got := mustBalance(t, p, source); !got.Equal(decimal.RequireFromString("100.00")) {
		t.Fatalf("source was partially debited: got %s, want 100.00", got)
	}
	if got := mustBalance(t, p, dest); !got.Equal(decimal.RequireFromString("50.00")) {
		t.Fatalf("destination changed after failed transfer: got %s, want 50.00", got)
	}
	if got := mustTotalBalance(t, p); !got.Equal(beforeTotal) {
		t.Fatalf("total balance changed after rollback: before=%s after=%s", beforeTotal, got)
	}

	pending, completed, failed := mustTransferStatusCounts(t, p)
	if pending != 0 || completed != 0 || failed != 1 {
		t.Fatalf("statuses after injected failure: pending=%d completed=%d failed=%d; want 0/0/1",
			pending, completed, failed)
	}
}

func TestTransferConcurrentDisjointPairs(t *testing.T) {
	p := newTestPostgres(t)

	const pairs = 32
	amount := decimal.RequireFromString("1.25")

	type pair struct {
		source int64
		dest   int64
	}
	accounts := make([]pair, 0, pairs)

	for i := 0; i < pairs; i++ {
		accounts = append(accounts, pair{
			source: mustInsertAccount(t, p, decimal.RequireFromString("100.00")),
			dest:   mustInsertAccount(t, p, decimal.Zero),
		})
	}

	start := make(chan struct{})
	errs := make(chan error, pairs)
	var wg sync.WaitGroup

	for _, pair := range accounts {
		pair := pair
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := p.Transfer(makeTransferForm(pair.source, pair.dest, amount))
			errs <- err
		}()
	}

	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("disjoint concurrent transfer failed: %v", err)
		}
	}

	for _, pair := range accounts {
		if got := mustBalance(t, p, pair.source); !got.Equal(decimal.RequireFromString("98.75")) {
			t.Fatalf("source %d balance=%s, want 98.75", pair.source, got)
		}
		if got := mustBalance(t, p, pair.dest); !got.Equal(decimal.RequireFromString("1.25")) {
			t.Fatalf("destination %d balance=%s, want 1.25", pair.dest, got)
		}
	}

	wantTotal := decimal.NewFromInt(pairs * 100)
	if got := mustTotalBalance(t, p); !got.Equal(wantTotal) {
		t.Fatalf("total balance=%s, want %s", got, wantTotal)
	}

	pending, completed, failed := mustTransferStatusCounts(t, p)
	if pending != 0 || completed != pairs || failed != 0 {
		t.Fatalf("statuses: pending=%d completed=%d failed=%d; want 0/%d/0",
			pending, completed, failed, pairs)
	}
}

func TestTransferConcurrentHotAccountPreservesInvariant(t *testing.T) {
	p := newTestPostgres(t)

	const requests = 64
	const destinations = 8

	source := mustInsertAccount(t, p, decimal.RequireFromString("10000.00"))
	dests := make([]int64, destinations)
	for i := range dests {
		dests[i] = mustInsertAccount(t, p, decimal.Zero)
	}

	amount := decimal.RequireFromString("10.00")
	beforeTotal := mustTotalBalance(t, p)

	start := make(chan struct{})
	var wg sync.WaitGroup
	var successes atomic.Int64
	var failures atomic.Int64

	for i := 0; i < requests; i++ {
		dest := dests[i%len(dests)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := p.Transfer(makeTransferForm(source, dest, amount)); err != nil {
				failures.Add(1)
				return
			}
			successes.Add(1)
		}()
	}

	close(start)
	wg.Wait()

	ok := successes.Load()
	bad := failures.Load()
	if ok+bad != requests {
		t.Fatalf("lost results: success=%d failed=%d requests=%d", ok, bad, requests)
	}

	wantSource := decimal.RequireFromString("10000.00").
		Sub(amount.Mul(decimal.NewFromInt(ok)))
	if got := mustBalance(t, p, source); !got.Equal(wantSource) {
		t.Fatalf("source balance=%s, want %s after %d committed transfers", got, wantSource, ok)
	}

	if got := mustTotalBalance(t, p); !got.Equal(beforeTotal) {
		t.Fatalf("money was created/lost under concurrency: before=%s after=%s", beforeTotal, got)
	}

	pending, completed, failed := mustTransferStatusCounts(t, p)
	if pending != 0 {
		t.Fatalf("%d transfers remained pending after all calls returned", pending)
	}
	if int64(completed) != ok {
		t.Fatalf("completed rows=%d, successful calls=%d", completed, ok)
	}
	if int64(failed) != bad {
		t.Fatalf("failed rows=%d, failed calls=%d", failed, bad)
	}

	t.Logf("hot-account concurrency: %d successful, %d serialization/transaction failures", ok, bad)
}

func TestTransferConcurrentOppositeDirectionsDoesNotDeadlock(t *testing.T) {
	p := newTestPostgres(t)

	const requests = 40
	a := mustInsertAccount(t, p, decimal.RequireFromString("1000.00"))
	b := mustInsertAccount(t, p, decimal.RequireFromString("1000.00"))
	beforeTotal := mustTotalBalance(t, p)

	start := make(chan struct{})
	done := make(chan struct{})
	var wg sync.WaitGroup
	var successes atomic.Int64
	var failures atomic.Int64

	for i := 0; i < requests; i++ {
		source, dest := a, b
		if i%2 == 1 {
			source, dest = b, a
		}
		wg.Add(1)
		go func(source, dest int64) {
			defer wg.Done()
			<-start
			if _, err := p.Transfer(makeTransferForm(source, dest, decimal.NewFromInt(1))); err != nil {
				failures.Add(1)
				return
			}
			successes.Add(1)
		}(source, dest)
	}

	close(start)
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("concurrent opposite-direction transfers did not finish within 15s; possible deadlock/stall")
	}

	if got := mustTotalBalance(t, p); !got.Equal(beforeTotal) {
		t.Fatalf("money was created/lost: before=%s after=%s", beforeTotal, got)
	}
	if mustBalance(t, p, a).IsNegative() || mustBalance(t, p, b).IsNegative() {
		t.Fatalf("an account became negative: a=%s b=%s", mustBalance(t, p, a), mustBalance(t, p, b))
	}

	pending, completed, failed := mustTransferStatusCounts(t, p)
	if pending != 0 {
		t.Fatalf("%d transfers remained pending", pending)
	}
	if completed+failed != requests {
		t.Fatalf("completed+failed=%d, want %d", completed+failed, requests)
	}
	if int64(completed) != successes.Load() || int64(failed) != failures.Load() {
		t.Fatalf("DB statuses do not match call results: completed=%d/%d failed=%d/%d",
			completed, successes.Load(), failed, failures.Load())
	}
}

func TestTransferLoadSmoke(t *testing.T) {
	if os.Getenv("ICHIHIME_LOAD_TEST") != "1" {
		t.Skip("set ICHIHIME_LOAD_TEST=1 to run the load smoke test")
	}

	p := newTestPostgres(t)

	const (
		pairs      = 32
		operations = 1000
		workers    = 32
	)

	type pair struct {
		a int64
		b int64
	}
	ps := make([]pair, pairs)
	for i := range ps {
		ps[i] = pair{
			a: mustInsertAccount(t, p, decimal.RequireFromString("100000.00")),
			b: mustInsertAccount(t, p, decimal.RequireFromString("100000.00")),
		}
	}

	beforeTotal := mustTotalBalance(t, p)
	jobs := make(chan int, workers*2)
	var wg sync.WaitGroup
	var successes atomic.Int64
	var failures atomic.Int64

	started := time.Now()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				pair := ps[n%len(ps)]
				source, dest := pair.a, pair.b
				if (n/len(ps))%2 == 1 {
					source, dest = pair.b, pair.a
				}
				if _, err := p.Transfer(makeTransferForm(source, dest, decimal.RequireFromString("0.01"))); err != nil {
					failures.Add(1)
				} else {
					successes.Add(1)
				}
			}
		}()
	}

	for i := 0; i < operations; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	elapsed := time.Since(started)

	if got := mustTotalBalance(t, p); !got.Equal(beforeTotal) {
		t.Fatalf("load test violated total-balance invariant: before=%s after=%s", beforeTotal, got)
	}

	pending, completed, failed := mustTransferStatusCounts(t, p)
	if pending != 0 {
		t.Fatalf("%d transfer rows remained pending", pending)
	}
	if completed+failed != operations {
		t.Fatalf("completed+failed=%d, want %d", completed+failed, operations)
	}
	if int64(completed) != successes.Load() || int64(failed) != failures.Load() {
		t.Fatalf("status/result mismatch: completed=%d/%d failed=%d/%d",
			completed, successes.Load(), failed, failures.Load())
	}

	rps := float64(operations) / elapsed.Seconds()
	t.Logf(
		"%d operations with %d workers in %s: %.0f requests/s; success=%d failed=%d",
		operations, workers, elapsed.Round(time.Millisecond), rps, successes.Load(), failures.Load(),
	)
}
