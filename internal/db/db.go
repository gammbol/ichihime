package db

import (
	"github.com/gammbol/ichihime/internal/storage"
	"github.com/shopspring/decimal"
)

type DBContract interface {
	Close()
	
	GetAllAccounts() ([]storage.Account, error)
	GetAllTransfers() ([]storage.Transfer, error)
	GetAccountById(int) (storage.Account, error)
	GetTransferById(int) (storage.Transfer, error)
	Transfer(int, int, decimal.Decimal) (storage.Transfer, error)
}