package db

import (
	"github.com/gammbol/ichihime/internal/storage"
)

type DBContract interface {
	Close()
	
	GetAllAccounts() ([]storage.Account, error)
	GetAllTransfers() ([]storage.Transfer, error)
	GetAccountById(int) (storage.Account, error)
	GetTransferById(int) (storage.Transfer, error)
	Transfer(storage.TransferForm) (storage.Transfer, error)
}