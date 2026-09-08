package db

import (
	"github.com/gammbol/ichihime/internal/storage"
)

type DBContract interface {
	Close()
	
	GetAllAccounts() ([]storage.Account, error)
	GetAllTransfers() ([]storage.Transfer, error)
	GetAccountById(id int) (storage.Account, error)
	GetTransferById(id int) (storage.Transfer, error)
}