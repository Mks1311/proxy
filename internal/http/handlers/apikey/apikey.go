package apikey

import (
	"net/http"

	"github.com/Mks1311/poolify/internal/database"
	"github.com/Mks1311/poolify/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
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

func GetAvailableKey(service string, serviceSpecificRateLimit int) (string, bool) {
	var apiKeyPool models.APIKeyPool
	if err := database.DB.Where("service = ? AND is_active = ? AND rate_limit >= ?", service, true, serviceSpecificRateLimit).Order("requests_today ASC").First(&apiKeyPool).Error; err != nil {
		return "", false
	}
	if err := ConsumeServiceKey(apiKeyPool.APIKey); err != nil {
		return "", false
	}
	return apiKeyPool.APIKey, true
}

func ConsumeServiceKey(apiKey string) error {
	return database.DB.Model(&models.APIKeyPool{}).
		Where("api_key = ?", apiKey).
		UpdateColumn("requests_today", gorm.Expr("requests_today + ?", 1)).
		Error
}
