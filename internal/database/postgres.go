package database

import (
	"log"
	"os"

	"github.com/Mks1311/poolify/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func ConnectPostgres() error {
	dsn := os.Getenv("DATABASE_URL")

	if dsn == "" {
		log.Fatal("DATABASE_URL not set")
	}

	database, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})

	if err != nil {
		log.Fatal("Failed to connect to database:", err)
		return err
	}

	DB = database
	log.Println("Database connection established")

	Migrate()
	return nil
}

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func Migrate() error {
	log.Println("Running database migrations...")

	err := DB.AutoMigrate(
		&models.User{},
		&models.APIKeyPool{},
		&models.TokenUsage{},
	)

	if err != nil {
		log.Fatal("Failed to migrate database:", err)
		return err
	}

	log.Println("Database migration completed")

	return nil
}
