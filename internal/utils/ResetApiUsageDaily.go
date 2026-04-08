package utils

import (
	"log"
	"time"

	"github.com/Mks1311/poolify/internal/database"
	"github.com/Mks1311/poolify/internal/models"
)

func ResetApiUsageDaily() {
	// Reset API key pool request counters
	err := database.DB.Model(&models.APIKeyPool{}).
		Where("requests_today > ?", 0).
		Update("requests_today", 0).Error

	if err != nil {
		log.Println("Failed to reset API usage:", err)
	} else {
		log.Println("API key pool usage reset successfully")
	}

	// Clean up token usage logs older than 30 days
	cutoff := time.Now().AddDate(0, 0, -30).Unix()
	result := database.DB.Where("created_at < ?", cutoff).Delete(&models.TokenUsage{})
	if result.Error != nil {
		log.Println("Failed to clean up old token usage logs:", result.Error)
	} else {
		log.Printf("Cleaned up %d old token usage records", result.RowsAffected)
	}
}
