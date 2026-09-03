package postgresdb

import (
	"context"
	"fmt"
	"log"

	"github.com/gammbol/ichihime/internal/storage"
	"github.com/jackc/pgx/v5"
)

type Postgres struct {
	db *pgx.Conn
}

func New(connectionString string) (*Postgres, error) {
	psql, err := pgx.Connect(context.Background(), connectionString)
	if err != nil {
		log.Fatalf("postgresql connection failure: %v", err)
	}

	pingErr := psql.Ping(context.Background())
	if pingErr != nil {
		log.Fatalf("postgresql ping failure: %v", err)
	}

	return &Postgres{psql}, nil
}

func (this *Postgres) Close() {
	this.db.Close(context.Background())
}

func (this *Postgres) GetAllAlbums() ([]storage.Album, error) {
	rows, queryErr := this.db.Query(context.Background(), "select * from album")
	if queryErr != nil {
		return nil, fmt.Errorf("GetAllAlbums: %v", queryErr)
	}

	albums, collectErr := pgx.CollectRows(rows, pgx.RowToStructByName[storage.Album])
	if collectErr != nil {
		return nil, fmt.Errorf("GetAllAlbums: %v", collectErr)
	}

	return albums, nil
}