package storage

import (
	"time"

	"github.com/shopspring/decimal"
)

type Account struct {
	ID					string					`json:"id"`
	Currency		string					`json:"currency"`
	Balance			decimal.Decimal	`json:"balance"`
	Created_at	time.Time				`json:"created_at"`
}

type Transfer struct {
	ID					string					`json:"id"`
	Source_ID		string					`json:"source_id"`
	Dest_ID			string					`json:"dest_id"`
	Amount			decimal.Decimal	`json:"amount"`
	Currency		string					`json:"currency"`
	Status			string					`json:"status"`	
	Created_at	time.Time				`json:"created_at"`
}

type TransferForm struct {
	Source			int							`form:"source" binding:"required"`
	Destination	int							`form:"dest" binding:"required"`
	Amount			decimal.Decimal	`form:"amount" binding:"required"`
}