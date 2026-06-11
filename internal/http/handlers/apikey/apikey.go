package apikey

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/Mks1311/poolify/internal/database"
	"github.com/Mks1311/poolify/internal/models"
	"github.com/Mks1311/poolify/internal/provider"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProviderChain is the global provider chain reference, set by main.go during init.
// Used for key validation when users submit new API keys.
var ProviderChain *provider.Chain

// TokensPerKeyContribution is how many tokens a user's daily limit increases
// when they contribute a valid API key.
const TokensPerKeyContribution = 200

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

	// 3. Validate service is a known provider
	if !provider.IsValidProvider(input.Service) {
		c.JSON(400, gin.H{
			"error":              fmt.Sprintf("Unknown service provider: %s", input.Service),
			"supported_services": provider.SupportedProviders(),
		})
		return
	}

	// 4. Get user from context
	userInterface, exists := c.Get("user")
	if !exists {
		c.JSON(500, gin.H{"error": "User not found in context"})
		c.Abort()
		return
	}

	user := userInterface.(*models.User)

	// 5. Validate the API key by making a dummy call to the provider
	if ProviderChain != nil {
		log.Printf("Validating %s API key for user %s...", input.Service, user.ID)
		if err := ProviderChain.ValidateKey(input.Service, input.ApiKey); err != nil {
			c.JSON(400, gin.H{
				"error":   fmt.Sprintf("API key validation failed: %s", err.Error()),
				"message": "Please provide a valid API key",
			})
			return
		}
		log.Printf("API key validated successfully for %s", input.Service)
	}

	// 6. Create API key pool entry
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

	// 7. Increase user's daily token limit as a reward for contributing a key
	if err := database.DB.Model(&models.User{}).
		Where("id = ?", user.ID).
		UpdateColumn("daily_limit", gorm.Expr("daily_limit + ?", TokensPerKeyContribution)).Error; err != nil {
		log.Printf("Warning: failed to increase daily limit for user %s: %v", user.ID, err)
	}

	// Refresh user to get the updated daily limit
	var updatedUser models.User
	database.DB.First(&updatedUser, "id = ?", user.ID)

	c.JSON(201, gin.H{
		"message":         "API key added successfully",
		"data":            apiKeyPool,
		"daily_limit_new": updatedUser.DailyLimit,
		"tokens_added":    TokensPerKeyContribution,
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
