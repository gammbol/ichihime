package postgresdb

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/gammbol/ichihime/internal/storage"

	pgxdecimal "github.com/jackc/pgx-shopspring-decimal"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

const (
    maxRetries = 10
    baseDelay  = 100 * time.Millisecond
    maxDelay   = 1000 * time.Millisecond
)

type Postgres struct {
	ctx context.Context
	poll *pgxpool.Pool
}

func New(connectionString string, ctx context.Context) *Postgres {
	config, confErr := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if confErr != nil {
		log.Fatalf("postgresql config parse failure: %v", confErr)
	}
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		pgxdecimal.Register(conn.TypeMap())

		return nil
	}

	poll, pollErr := pgxpool.NewWithConfig(ctx, config)
	if pollErr != nil {
		log.Fatalf("postgresql poll creation failure: %v", pollErr)
	}

	pingErr := poll.Ping(ctx)
	if pingErr != nil {
		log.Fatalf("postgres ping failure: %v", pingErr)
	}

	return &Postgres{
		poll: poll, 
		ctx: ctx,
	}
}

func (this *Postgres) Close() {
	this.poll.Close()
}

func (this *Postgres) GetAllAccounts() ([]storage.Account, error) {
	rows, queryErr := this.poll.Query(this.ctx, "select * from accounts")
	if queryErr != nil {
		return nil, fmt.Errorf("GetAllAccounts: %v", queryErr)
	}

	accounts, collectErr := pgx.CollectRows(rows, pgx.RowToStructByName[storage.Account])
	if collectErr != nil {
		return nil, fmt.Errorf("GetAllAccounts: %v", collectErr)
	}

	return accounts, nil
}

func (this *Postgres) GetAccountById(id int) (storage.Account, error) {
	row, queryErr := this.poll.Query(this.ctx, "select * from accounts where id = $1", id)
	if queryErr != nil {
		return storage.Account{}, fmt.Errorf("GetAccountById: %v", queryErr)
	}

	account, collectErr := pgx.CollectExactlyOneRow(row, pgx.RowToStructByName[storage.Account])
	if collectErr != nil {
		return storage.Account{}, fmt.Errorf("GetAllAccounts: %v", collectErr)
	}

	return account, nil
}

func (this *Postgres) GetAllTransfers() ([]storage.Transfer, error) {
	rows, queryErr := this.poll.Query(this.ctx, "select * from transfers")
	if queryErr != nil {
		return nil, fmt.Errorf("GetAllTransfers: %v", queryErr)
	}

	transfers, collectErr := pgx.CollectRows(rows, pgx.RowToStructByName[storage.Transfer])
	if collectErr != nil {
		return nil, fmt.Errorf("GetAllTransfers: %v", collectErr)
	}

	return transfers, nil
}

func (this *Postgres) GetTransferById(id int) (storage.Transfer, error) {
	row, queryErr := this.poll.Query(this.ctx, "select * from transfers where id = $1", id)
	if queryErr != nil {
		return storage.Transfer{}, fmt.Errorf("GetTransferById: %v", queryErr)
	}

	transfer, collectErr := pgx.CollectExactlyOneRow(row, pgx.RowToStructByName[storage.Transfer])
	if collectErr != nil {
		return storage.Transfer{}, fmt.Errorf("GetTransferById: %v", collectErr)
	}

	return transfer, nil
}

func validateForm(tf storage.TransferForm) error {
	if tf.Source <= 0 {
		return fmt.Errorf("source id cannot be negative or zero")
	}
	if tf.Destination <= 0 {
		return fmt.Errorf("destination id cannot be negative or zero")
	}
	if tf.Amount.Sign() <= 0 {
		return fmt.Errorf("amount cannot be negative or zero")
	}
	if tf.Source == tf.Destination {
		return fmt.Errorf("source and destination ids cannot be equal")
	}

	return nil
}

func transactionHandler(transferId int64, transferForm storage.TransferForm, this Postgres) func (pgx.Tx) error {
	return func (tx pgx.Tx) error {
		// _, lockErr := tx.Exec(
		// 	this.ctx, 
		// 	"select 1 from accounts where id in ($1, $2) order by id FOR NO KEY UPDATE",
		// 	transferForm.Source,
		// 	transferForm.Destination,
		// )
		// if lockErr != nil {
		// 	return fmt.Errorf("Transfer (lock): %v", lockErr)
		// }

		var sourceBalance decimal.Decimal
		queryRowErr := tx.QueryRow(
			this.ctx,
			"select (balance) from accounts where id=$1",
			transferForm.Source,
		).Scan(&sourceBalance)
		if queryRowErr != nil {
			return fmt.Errorf("Transfer (select source balance): %w", queryRowErr)
		}

		if transferForm.Amount.Compare(sourceBalance) > 0 {
			return fmt.Errorf("Transfer (source balance validation): not enough money to make the transfer")
		}
		
		_, execErr := tx.Exec(
			this.ctx,
			"update accounts " +
			"set balance = balance - $1 " +
			"where id=$2",
			transferForm.Amount,
			transferForm.Source,
		)
		if execErr != nil {
			return fmt.Errorf("Transfer (subtract): %w", execErr)
		}

		_, execErr = tx.Exec(
			this.ctx,
			"update accounts " +
			"set balance = balance + $1 " +
			"where id=$2",
			transferForm.Amount,
			transferForm.Destination,
		)
		if execErr != nil {
			return fmt.Errorf("Transfer (add): %w", execErr)
		}

		_, execErr = tx.Exec(
			this.ctx,
			"update transfers " +
			"set status='completed' " +
			"where id=$1 ",
			transferId,
		)
		if execErr != nil {
			return fmt.Errorf("Transfer (complete transfer): %w", execErr)
		}

		return nil
	}
}

func isRetryableError(err error) bool {
	var pgErr *pgconn.PgError

	if !errors.As(err, &pgErr) {
		return false
	}

	switch pgErr.Code {
	case "40001":
		return true

	case "40P01":
		return true

	default:
		return false
	}
}

func (this *Postgres) Transfer(transferForm storage.TransferForm) (storage.Transfer, error) {
	err := validateForm(transferForm)
	if err != nil {
		return storage.Transfer{}, fmt.Errorf("Transfer: %v", err)
	}

	var transferId int64

	execErr := this.poll.QueryRow(
		this.ctx,
		"insert into transfers " +
		"(source_id, dest_id, currency, amount, status) " +
		"values ($1, $2, (select (currency) from accounts where id=$2), $3, 'pending') " +
		"returning id",
		transferForm.Source,
		transferForm.Destination,
		transferForm.Amount,
	).Scan(&transferId)
	if execErr != nil {
		return storage.Transfer{}, fmt.Errorf("Transfer (insert transfer): %v", execErr)
	}
	// transferRes, collectErr := pgx.CollectExactlyOneRow(row, pgx.RowToStructByName[storage.Transfer])
	// if collectErr != nil {
	// 	return storage.Transfer{}, fmt.Errorf("Transfer (parse transfer): %v", collectErr)
	// }
	
	var transactionErr error
	for retries := 0; retries < maxRetries; retries++ {
		transactionErr = pgx.BeginTxFunc(
			this.ctx,
			this.poll,
			pgx.TxOptions{
				IsoLevel: pgx.Serializable,
				AccessMode: pgx.ReadWrite,
			},
			transactionHandler(transferId, transferForm, *this),
		)
		if transactionErr != nil && isRetryableError(transactionErr) {
			backoff := baseDelay * time.Duration(1<<retries)
			if backoff > maxDelay {
					backoff = maxDelay
			}

			delay := time.Duration(rand.Int63n(int64(backoff)))

			log.Printf(
					"transaction conflict, retrying (attempt %d, delay %s): %v",
					retries+1,
					delay,
					transactionErr,
			)

			timer := time.NewTimer(delay)

			select {
			case <-timer.C:
					continue

			case <-(this.ctx).Done():
					if !timer.Stop() {
							<-timer.C
					}
					return storage.Transfer{}, (this.ctx).Err()
			}
		}
		
		break
	}
	if transactionErr != nil {
		// log.Printf("ERROR DEBUGGING: %v\n", transactionErr)
		_, execErr = this.poll.Exec(
			this.ctx,
			"update transfers " +
			"set status='failed' " +
			"where id=$1",
			transferId,
		)
		if execErr != nil {
			return storage.Transfer{}, fmt.Errorf("Transfer (fail transfer query): %v", execErr)
		}
		return storage.Transfer{}, fmt.Errorf("Transfer (fail transfer): %v", transactionErr)
	}

	row, queryErr := this.poll.Query(
		this.ctx,
		"select * from transfers where id = $1",
		transferId,
	)
	if queryErr != nil {
		return storage.Transfer{}, fmt.Errorf("Transfer (final select): %v", queryErr)
	}
	transferRes, collectErr := pgx.CollectExactlyOneRow(row, pgx.RowToStructByName[storage.Transfer])
	if collectErr != nil {
		return storage.Transfer{}, fmt.Errorf("Transfer (final collect): %v", collectErr)
	}

	return transferRes, nil
}