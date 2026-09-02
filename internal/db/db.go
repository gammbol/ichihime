package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type connConf struct {
	connString 		string
	isConnected		bool

	conn *pgx.Conn
}

func (c connConf) Init() error {
	var err error

	c.conn, err = pgx.Connect(context.Background(), c.connString)
	if err != nil {
		return fmt.Errorf("Error connecting to the database: %s", err)
	}

	pingErr := c.conn.Ping(context.Background())
	if pingErr != nil {
		c.conn.Close(context.Background())
		return fmt.Errorf("Error establishing the connection with the database: %s", err)
	}

	return nil
}

func (c connConf) Close() error {
	if c.isConnected {
		c.conn.Close(context.Background())
		return nil
	}

	return errors.New("connection is already closed")
}