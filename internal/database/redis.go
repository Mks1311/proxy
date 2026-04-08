package database

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/redis/go-redis/v9"
)

var RedisClient *redis.Client
var Ctx = context.Background()

func InitRedis() error {
	redisURL := os.Getenv("REDIS_URL")

	if redisURL == "" {
		log.Fatal("REDIS_URL not set")
	}

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatal("Failed to parse Redis URL:", err)
		return err
	}

	RedisClient = redis.NewClient(opt)

	// Test connection
	_, err = RedisClient.Ping(Ctx).Result()
	if err != nil {
		log.Fatal("Failed to connect to Redis:", err)
		return err
	}

	fmt.Println("✅ Connected to Redis")
	return nil
}
