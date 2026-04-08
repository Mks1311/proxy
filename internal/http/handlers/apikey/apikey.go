package apikey

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Mks1311/poolify/internal/database"
	"github.com/Mks1311/poolify/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func AddApiKey(c *gin.Context) {
	var input struct {
		Service string `json:"service"`
		ApiKey  string `json:"api_key"`
	}

	if err := c.BindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request body",
		})
		return
	}

	// 2. Validate input
	if input.Service == "" {
		c.JSON(400, gin.H{"error": "Service cannot be empty"})
		return
	}

	if input.ApiKey == "" {
		c.JSON(400, gin.H{"error": "API Key cannot be empty"})
		return
	}

	// 3. Get user from context
	userInterface, exists := c.Get("user")
	if !exists {
		c.JSON(500, gin.H{"error": "User not found in context"})
		c.Abort()
		return
	}

	user := userInterface.(*models.User)

	// 4. Create API key pool
	apiKeyPool := models.APIKeyPool{
		Service:       input.Service,
		APIKey:        input.ApiKey,
		OwnerUserId:   &user.ID,
		RequestsToday: 0,
		IsActive:      true,
	}

	if err := database.DB.Create(&apiKeyPool).Error; err != nil {
		c.JSON(500, gin.H{"error": "Failed to create API key pool"})
		return
	}

	c.JSON(201, gin.H{
		"message": "API key pool created successfully",
		"data":    apiKeyPool,
	})
}

// ConsumeAvailableKey finds a key that is:
//   - active
//   - under its daily rate limit
//   - NOT in cooldown (checked via Redis)
//
// Returns (apiKey, keyID, ok). The keyID is needed for CooldownKey.
func ConsumeAvailableKey(service string) (string, uint, bool) {
	// Try up to 5 keys in case some are in cooldown
	for attempt := 0; attempt < 5; attempt++ {
		var apiKeyPool models.APIKeyPool

		// Start a transaction to make select + update atomic
		tx := database.DB.Begin()
		defer func() {
			if r := recover(); r != nil {
				tx.Rollback()
			}
		}()

		// Lock the row for update (prevents race condition)
		// Use OFFSET to skip keys we've already tried (cooldown ones will be skipped below)
		if err := tx.Where("service = ? AND is_active = ? AND requests_today < rate_limit",
			service, true).
			Order("requests_today ASC").
			Offset(attempt).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&apiKeyPool).Error; err != nil {
			tx.Rollback()
			return "", 0, false
		}

		// Check if this key is in cooldown (Redis check)
		cooldownKey := fmt.Sprintf("cooldown:apikey:%d", apiKeyPool.ID)
		ctx := context.Background()
		exists, _ := database.RedisClient.Exists(ctx, cooldownKey).Result()
		if exists > 0 {
			// Key is in cooldown, skip it
			tx.Rollback()
			continue
		}

		// Increment request count
		if err := tx.Model(&models.APIKeyPool{}).
			Where("id = ?", apiKeyPool.ID).
			UpdateColumn("requests_today", gorm.Expr("requests_today + ?", 1)).Error; err != nil {
			tx.Rollback()
			return "", 0, false
		}

		// Commit transaction
		if err := tx.Commit().Error; err != nil {
			return "", 0, false
		}

		return apiKeyPool.APIKey, apiKeyPool.ID, true
	}

	return "", 0, false
}

// CooldownKey marks an API key as in cooldown for the given duration.
// While in cooldown, ConsumeAvailableKey will skip this key.
func CooldownKey(keyID uint, duration time.Duration) {
	cooldownKey := fmt.Sprintf("cooldown:apikey:%d", keyID)
	ctx := context.Background()
	database.RedisClient.Set(ctx, cooldownKey, "1", duration)
}
