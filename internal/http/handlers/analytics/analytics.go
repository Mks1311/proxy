package analytics

import (
	"net/http"
	"time"

	"github.com/Mks1311/poolify/internal/database"
	"github.com/Mks1311/poolify/internal/models"
	"github.com/Mks1311/poolify/internal/ratelimit"
	"github.com/gin-gonic/gin"
)

type UsageSummary struct {
	Model            string `json:"model"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	CreatedAt        int64  `json:"created_at"`
}

// GetUsage returns the authenticated user's token usage stats for today.
func GetUsage(c *gin.Context) {
	// 1. Get user from context
	userInterface, exists := c.Get("user")
	if !exists {
		c.JSON(500, gin.H{"error": "User not found in context"})
		return
	}
	user := userInterface.(*models.User)

	// 2. Get remaining token budget from Redis
	limiter := ratelimit.NewTokenBucket(database.RedisClient)
	remaining, resetTime, err := limiter.GetRemainingTokens(user.ID, user.DailyLimit)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to get token budget"})
		return
	}

	tokensUsed := user.DailyLimit - remaining
	if tokensUsed < 0 {
		tokensUsed = 0
	}

	// 3. Get recent usage logs from Postgres (last 50 entries, today only)
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var recentUsage []UsageSummary
	database.DB.Model(&models.TokenUsage{}).
		Where("user_id = ? AND created_at >= ?", user.ID, startOfDay.Unix()).
		Order("created_at DESC").
		Limit(50).
		Find(&recentUsage)

	// 4. Get aggregated stats per model for today
	type ModelStats struct {
		Model            string `json:"model"`
		TotalRequests    int64  `json:"total_requests"`
		TotalPrompt      int64  `json:"total_prompt_tokens"`
		TotalCompletion  int64  `json:"total_completion_tokens"`
		TotalTokensUsed  int64  `json:"total_tokens"`
	}

	var modelStats []ModelStats
	database.DB.Model(&models.TokenUsage{}).
		Select("model, COUNT(*) as total_requests, SUM(prompt_tokens) as total_prompt, SUM(completion_tokens) as total_completion, SUM(total_tokens) as total_tokens_used").
		Where("user_id = ? AND created_at >= ?", user.ID, startOfDay.Unix()).
		Group("model").
		Find(&modelStats)

	c.JSON(http.StatusOK, gin.H{
		"daily_limit":      user.DailyLimit,
		"tokens_used":      tokensUsed,
		"tokens_remaining": remaining,
		"reset_time":       resetTime.Format(time.RFC3339),
		"by_model":         modelStats,
		"recent_usage":     recentUsage,
	})
}
