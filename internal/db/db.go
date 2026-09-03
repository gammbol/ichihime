package db

import (
	"github.com/gammbol/ichihime/internal/storage"
)

type DBContract interface {
	Close()
	
	GetAllAlbums() ([]storage.Album, error)
}