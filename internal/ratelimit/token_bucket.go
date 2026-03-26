package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

type TokenBucket struct {
	redisClient *redis.Client
	ctx         context.Context
}

func NewTokenBucket(redisClient *redis.Client) *TokenBucket {
	return &TokenBucket{
		redisClient: redisClient,
		ctx:         context.Background(),
	}
}

// ConsumeToken attempts to consume 1 token for the user
// Returns: (allowed bool, remaining tokens, reset time, error)
func (tb *TokenBucket) ConsumeToken(userID string, dailyLimit int) (bool, int, time.Time, error) {
	key := fmt.Sprintf("ratelimit:user:%s:daily", userID)

	// Calculate TTL until midnight
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	ttl := time.Until(midnight)

	// Try to initialize key only if it doesn't exist (atomic)
	set, err := tb.redisClient.SetNX(tb.ctx, key, dailyLimit, ttl).Result()
	if err != nil {
		return false, 0, time.Time{}, err
	}

	var currentTokens int

	if set {
		// Key was just created
		currentTokens = dailyLimit
	} else {
		// Key already exists → fetch current value
		tokensStr, err := tb.redisClient.Get(tb.ctx, key).Result()
		if err != nil {
			return false, 0, time.Time{}, err
		}

		currentTokens, err = strconv.Atoi(tokensStr)
		if err != nil {
			return false, 0, time.Time{}, err
		}
	}

	// Check if tokens available
	if currentTokens <= 0 {
		ttl, err := tb.redisClient.TTL(tb.ctx, key).Result()
		if err != nil {
			return false, 0, time.Time{}, err
		}

		resetTime := time.Now().Add(ttl)
		return false, 0, resetTime, nil
	}

	// Atomically decrement token
	newTokens, err := tb.redisClient.Decr(tb.ctx, key).Result()
	if err != nil {
		return false, 0, time.Time{}, err
	}

	// Get reset time
	ttl, err = tb.redisClient.TTL(tb.ctx, key).Result()
	if err != nil {
		return false, 0, time.Time{}, err
	}

	resetTime := time.Now().Add(ttl)

	return true, int(newTokens), resetTime, nil
}

// GetRemainingTokens returns the current token count without consuming
func (tb *TokenBucket) GetRemainingTokens(userID uint, dailyLimit int) (int, time.Time, error) {
	key := fmt.Sprintf("ratelimit:user:%d:daily", userID)

	tokensStr, err := tb.redisClient.Get(tb.ctx, key).Result()

	if err == redis.Nil {
		// No key = full quota available
		now := time.Now()
		midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		return dailyLimit, midnight, nil
	} else if err != nil {
		return 0, time.Time{}, err
	}

	var currentTokens int
	currentTokens, err = strconv.Atoi(tokensStr)
	if err != nil {
		return 0, time.Time{}, err
	}

	ttl, _ := tb.redisClient.TTL(tb.ctx, key).Result()
	resetTime := time.Now().Add(ttl)

	return currentTokens, resetTime, nil
}

// ResetUserQuota manually resets a user's quota (useful for testing or admin features)
func (tb *TokenBucket) ResetUserQuota(userID uint) error {
	key := fmt.Sprintf("ratelimit:user:%d:daily", userID)
	return tb.redisClient.Del(tb.ctx, key).Err()
}
