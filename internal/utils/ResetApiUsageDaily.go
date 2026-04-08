package utils

import (
	"log"

	"github.com/Mks1311/poolify/internal/database"
	"github.com/Mks1311/poolify/internal/models"
)

func ResetApiUsageDaily() {
	err := database.DB.Model(&models.APIKeyPool{}).
		Where("requests_today > ?", 0).
		Update("requests_today", 0).Error

	if err != nil {
		log.Println("Failed to reset API usage:", err)
		return
	}

	log.Println("API usage reset successfully")
}
