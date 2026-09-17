package rediscache

import (
	"context"
	"log"

	"github.com/gammbol/ichihime/internal/storage"
	"github.com/redis/go-redis/v9"
)

type Redis struct {
	// ctx context.Context
	client *redis.Client
}

func New(connectionString string) *Redis {
	opt, err := redis.ParseURL(connectionString)
	if err != nil {
		log.Fatalf("Redis (new): %v", err)
	}

	client := redis.NewClient(opt)

	return &Redis{client}
}

func (this *Redis) Close() {
	this.Close()
}

func (this *Redis) Get(id *storage.Idempotency) error {
	val, err := this.client.Get(context.Background(), id.Key).Result()
	if err != nil {
		return err
	}

	id.Status = val
	return nil
}

func (this *Redis) Set(id storage.Idempotency) error {
	return this.client.Set(context.Background(), id.Key, id.Status, 0).Err()
}