package cache

import (
	"context"
	"fmt"
	"time"

	"agentmem/internal/config"

	"github.com/redis/go-redis/v9"
)

type RedisCache struct {
	client *redis.Client
}

func NewRedisCacheFromConfig(cacheConfig config.CacheConfig) (*RedisCache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cacheConfig.RedisAddr,
		Password: cacheConfig.RedisPassword,
		DB:       cacheConfig.RedisDB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &RedisCache{client: client}, nil
}

func (r *RedisCache) Close() error {
	return r.client.Close()
}
