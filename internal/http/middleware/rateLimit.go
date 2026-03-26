package middleware

import (
	"fmt"
	"time"

	"github.com/Mks1311/poolify/internal/database"
	"github.com/Mks1311/poolify/internal/models"
	"github.com/Mks1311/poolify/internal/ratelimit"
	"github.com/gin-gonic/gin"
)

func RateLimitMiddleware() gin.HandlerFunc {

	limiter := ratelimit.NewTokenBucket(database.RedisClient)

	return func(c *gin.Context) {
		// Get user from context (set by AuthMiddleware)
		userInterface, exists := c.Get("user")
		if !exists {
			c.JSON(500, gin.H{"error": "User not found in context"})
			c.Abort()
			return
		}

		user := userInterface.(*models.User)

		// Try to consume a token
		allowed, remaining, resetTime, err := limiter.ConsumeToken(user.ID, user.DailyLimit)
		if err != nil {
			c.JSON(500, gin.H{"error": "Rate limit check failed"})
			c.Abort()
			return
		}

		// Add rate limit headers to response
		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", user.DailyLimit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", resetTime.Unix()))

		if !allowed {
			// Rate limit exceeded
			retryAfter := int(time.Until(resetTime).Seconds())
			c.Header("Retry-After", fmt.Sprintf("%d", retryAfter))

			c.JSON(429, gin.H{
				"error":       "Rate limit exceeded",
				"message":     fmt.Sprintf("You have exceeded your daily quota of %d requests", user.DailyLimit),
				"retry_after": retryAfter,
				"reset_time":  resetTime.Format(time.RFC3339),
				"limit":       user.DailyLimit,
				"remaining":   0,
			})
			c.Abort()
			return
		}

		// Request allowed, continue
		c.Next()
	}
}
