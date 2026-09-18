package rediscache

import (
	"context"
	"log"

	"github.com/gammbol/ichihime/internal/storage"
	"github.com/redis/go-redis/v9"
)

type Redis struct {
	ctx context.Context
	client *redis.Client
}

func New(connectionString string, ctx context.Context) *Redis {
	opt, err := redis.ParseURL(connectionString)
	if err != nil {
		log.Fatalf("Redis (new): %v", err)
	}

	client := redis.NewClient(opt)

	return &Redis{
		client: client, 
		ctx: ctx,
	}
}

func (this *Redis) Close() {
	this.client.Close()
}

func (this *Redis) Get(id *storage.Idempotency) error {
	val, err := this.client.Get(this.ctx, id.Key).Result()
	id.Status = val
	return err
}

func (this *Redis) SetNx(id storage.Idempotency) (bool, error) {
	// return this.client.Set(context.Background(), id.Key, id.Status, 0).Err()
	return this.client.SetNX(context.Background(), id.Key, id.Status, 0).Result()
}

func (this *Redis) Set(id storage.Idempotency) error {
	return this.client.Set(context.Background(), id.Key, id.Status, 0).Err()
}