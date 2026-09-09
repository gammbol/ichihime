package postgresdb

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/gammbol/ichihime/internal/storage"
	"github.com/shopspring/decimal"

	pgxdecimal "github.com/jackc/pgx-shopspring-decimal"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Postgres struct {
	poll *pgxpool.Pool
}

func New(connectionString string) (*Postgres, error) {
	config, confErr := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if confErr != nil {
		log.Fatalf("postgresql config parse failure: %v", confErr)
	}
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		pgxdecimal.Register(conn.TypeMap())

		return nil
	}

	poll, pollErr := pgxpool.NewWithConfig(context.Background(), config)
	if pollErr != nil {
		log.Fatalf("postgresql poll creation failure: %v", pollErr)
	}

	pingErr := poll.Ping(context.Background())
	if pingErr != nil {
		log.Fatalf("postgres ping failure: %v", pingErr)
	}

	return &Postgres{poll}, nil
}

func (this *Postgres) Close() {
	this.poll.Close()
}

func (this *Postgres) GetAllAccounts() ([]storage.Account, error) {
	rows, queryErr := this.poll.Query(context.Background(), "select * from accounts")
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
	row, queryErr := this.poll.Query(context.Background(), "select * from accounts where id = $1", id)
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
	rows, queryErr := this.poll.Query(context.Background(), "select * from transfers")
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
	row, queryErr := this.poll.Query(context.Background(), "select * from transfers where id = $1", id)
	if queryErr != nil {
		return storage.Transfer{}, fmt.Errorf("GetTransferById: %v", queryErr)
	}

	transfer, collectErr := pgx.CollectExactlyOneRow(row, pgx.RowToStructByName[storage.Transfer])
	if collectErr != nil {
		return storage.Transfer{}, fmt.Errorf("GetTransferById: %v", collectErr)
	}

	return transfer, nil
}

func (this *Postgres) Transfer(source, dest int, amount decimal.Decimal) (storage.Transfer, error) {
	if source <= 0 {
		return storage.Transfer{}, fmt.Errorf("Transfer: source id cannot be negative or zero")
	}
	if dest <= 0 {
		return storage.Transfer{}, fmt.Errorf("Transfer: destination id cannot be negative or zero")
	}
	if amount.Sign() <= 0 {
		return storage.Transfer{}, fmt.Errorf("Transfer: amount cannot be negative or zero")
	}

	var transferId int64
	var transferRes storage.Transfer

	execErr := this.poll.QueryRow(
		context.Background(),
		"insert into transfers " +
		"(source_id, dest_id, currency, amount, status) " +
		"values ($1, $2, (select (currency) from accounts where id=$2), $3, 'pending') " +
		"returning id",
		source,
		dest,
		amount,
	).Scan(&transferId)
	if execErr != nil {
		return storage.Transfer{}, fmt.Errorf("Transfer (insert transfer): %v", execErr)
	}
	// transferRes, collectErr := pgx.CollectExactlyOneRow(row, pgx.RowToStructByName[storage.Transfer])
	// if collectErr != nil {
	// 	return storage.Transfer{}, fmt.Errorf("Transfer (parse transfer): %v", collectErr)
	// }
	

	transactionErr := pgx.BeginTxFunc(
		context.Background(),
		this.poll,
		pgx.TxOptions{
			IsoLevel: pgx.Serializable,
			AccessMode: pgx.ReadWrite,
		},
		func (tx pgx.Tx) error {
			_, execErr := tx.Exec(
				context.Background(),
				"update accounts " +
				"set balance = balance - $1 " +
				"where id=$2",
				amount,
				source,
			)
			if execErr != nil {
				return fmt.Errorf("Transfer (subtract): %v", execErr)
			}

			_, execErr = tx.Exec(
				context.Background(),
				"update accounts " +
				"set balance = balance + $1 " +
				"where id=$2",
				amount,
				dest,
			)
			if execErr != nil {
				return fmt.Errorf("Transfer (add): %v", execErr)
			}

			row, execErr := tx.Query(
				context.Background(),
				"update transfers " +
				"set status='completed' " +
				"where id=$1 " +
				"returning *",
				transferId,
			)
			if execErr != nil {
				return fmt.Errorf("Transfer (complete transfer): %v", execErr)
			}
			var collectErr error
			transferRes, collectErr = pgx.CollectExactlyOneRow(row, pgx.RowToStructByName[storage.Transfer])
			if collectErr != nil {
				return fmt.Errorf("Transfer (parse complete transfer): %v", collectErr)
			}

			return nil
		},
	)
	if transactionErr != nil {
		_, execErr = this.poll.Exec(
			context.Background(),
			"update transfers " +
			"set status='failed' " +
			"where id=$1",
			transferId,
		)
		if execErr != nil {
			return storage.Transfer{}, fmt.Errorf("Transfer (fail transfer query): %v", execErr)
		}
		return storage.Transfer{}, fmt.Errorf("Transfer (fail tranfer): %v", transactionErr)
	}

	return transferRes, nil
}