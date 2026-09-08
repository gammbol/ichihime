package postgresdb

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/gammbol/ichihime/internal/storage"

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