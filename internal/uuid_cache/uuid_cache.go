package uuidcache

import "github.com/gammbol/ichihime/internal/storage"

type UUIDCacheContract interface {
	Close()

	Get(*storage.Idempotency) error
	Set(storage.Idempotency) error
}