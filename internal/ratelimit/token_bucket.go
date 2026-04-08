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

// CheckBudget checks if the user has any remaining token budget WITHOUT consuming.
// This is called pre-request as a gate check. Actual deduction happens post-response.
// Returns: (allowed bool, remaining tokens, reset time, error)
func (tb *TokenBucket) CheckBudget(userID string, dailyLimit int) (bool, int, time.Time, error) {
	key := fmt.Sprintf("ratelimit:user:%s:daily_tokens", userID)

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
		// Key was just created — full budget available
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

	// Get reset time from TTL
	remainingTTL, err := tb.redisClient.TTL(tb.ctx, key).Result()
	if err != nil {
		return false, 0, time.Time{}, err
	}
	resetTime := time.Now().Add(remainingTTL)

	// Check if tokens available
	if currentTokens <= 0 {
		return false, 0, resetTime, nil
	}

	return true, currentTokens, resetTime, nil
}

// DeductTokens decreases the user's token budget by the given amount.
// Called post-response after we know the actual token cost.
// Returns: (newRemaining int, error)
func (tb *TokenBucket) DeductTokens(userID string, tokens int, dailyLimit int) (int, error) {
	key := fmt.Sprintf("ratelimit:user:%s:daily_tokens", userID)

	// Ensure the key exists (in case of race condition on first req)
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	ttl := time.Until(midnight)

	tb.redisClient.SetNX(tb.ctx, key, dailyLimit, ttl)

	// Atomically decrement by the token count
	newVal, err := tb.redisClient.DecrBy(tb.ctx, key, int64(tokens)).Result()
	if err != nil {
		return 0, err
	}

	return int(newVal), nil
}

// GetRemainingTokens returns the current token count without consuming.
func (tb *TokenBucket) GetRemainingTokens(userID string, dailyLimit int) (int, time.Time, error) {
	key := fmt.Sprintf("ratelimit:user:%s:daily_tokens", userID)

	tokensStr, err := tb.redisClient.Get(tb.ctx, key).Result()

	if err == redis.Nil {
		// No key = full quota available
		now := time.Now()
		midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		return dailyLimit, midnight, nil
	} else if err != nil {
		return 0, time.Time{}, err
	}

	currentTokens, err := strconv.Atoi(tokensStr)
	if err != nil {
		return 0, time.Time{}, err
	}

	ttl, _ := tb.redisClient.TTL(tb.ctx, key).Result()
	resetTime := time.Now().Add(ttl)

	return currentTokens, resetTime, nil
}

// ResetUserQuota manually resets a user's quota (useful for testing or admin features)
func (tb *TokenBucket) ResetUserQuota(userID string) error {
	key := fmt.Sprintf("ratelimit:user:%s:daily_tokens", userID)
	return tb.redisClient.Del(tb.ctx, key).Err()
}
