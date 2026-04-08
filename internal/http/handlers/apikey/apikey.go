package apikey

import (
	"net/http"

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

func ConsumeAvailableKey(service string) (string, bool) {
	var apiKeyPool models.APIKeyPool

	// Start a transaction to make select + update atomic
	tx := database.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Lock the row for update (prevents race condition)
	if err := tx.Where("service = ? AND is_active = ? AND requests_today < rate_limit",
		service, true).
		Order("requests_today ASC").
		Clauses(clause.Locking{Strength: "UPDATE"}). // FOR UPDATE lock
		First(&apiKeyPool).Error; err != nil {
		tx.Rollback()
		return "", false
	}

	// Increment request count
	if err := tx.Model(&models.APIKeyPool{}).
		Where("id = ?", apiKeyPool.ID).
		UpdateColumn("requests_today", gorm.Expr("requests_today + ?", 1)).Error; err != nil {
		tx.Rollback()
		return "", false
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		return "", false
	}

	return apiKeyPool.APIKey, true
}
