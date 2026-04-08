package cache

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/Mks1311/poolify/internal/database"
)

var DefaultTTL = 5 * time.Minute

// GenerateKey creates a SHA-256 hash of the request payload for use as a cache key.
func GenerateKey(payload []byte) string {
	hash := sha256.Sum256(payload)
	return fmt.Sprintf("cache:%x", hash)
}

// Get retrieves a cached response from Redis. Returns the cached bytes and true if found.
func Get(key string) ([]byte, bool) {
	ctx := context.Background()
	val, err := database.RedisClient.Get(ctx, key).Bytes()
	if err != nil {
		return nil, false
	}
	return val, true
}

// Set stores a response in Redis with the given TTL.
func Set(key string, response []byte, ttl time.Duration) {
	ctx := context.Background()
	database.RedisClient.Set(ctx, key, response, ttl)
}
