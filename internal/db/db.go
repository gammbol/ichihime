package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/gammbol/ichihime/internal/storage"
	"github.com/jackc/pgx/v5"
)

type ConnConf struct {
	connString 		string
	isConnected		bool

	Conn *pgx.Conn
}

func (c *ConnConf) Init(cs string) {
	c.connString = cs
}

func (c *ConnConf) GetAllAlbums() ([]storage.Album, error) {
	defer c.Close()
	err := c.Open()
	if err != nil {
		return nil, fmt.Errorf("GetAllAlbums: %v", err)
	}

	rows, err := c.Conn.Query(context.Background(), "select * from album")
	if err != nil {
		return nil, fmt.Errorf("GetAllAlbums: %v", err)
	}

	albums, err := pgx.CollectRows(rows, pgx.RowToStructByName[storage.Album])
	if err != nil {
		return nil, fmt.Errorf("GetAllAlbums: %v", err)
	}

	return albums, nil
}

func (c *ConnConf) Open() error {
	var err error
	c.Conn, err = pgx.Connect(context.Background(), c.connString)
	if err != nil {
		return fmt.Errorf("Error connecting to the database: %s", err)
	}

	c.isConnected = true
	return nil
}

func (c *ConnConf) Close() error {
	if c.isConnected {
		c.Conn.Close(context.Background())
		c.isConnected = false
		return nil
	}

	return errors.New("connection is already closed")
}