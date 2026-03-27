package database

import (
	"fmt"
	"log"
	"os"

	"github.com/Mks1311/poolify/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func ConnectPostgres() error {
	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := os.Getenv("DB_PASSWORD") // No default for password
	dbName := getEnv("DB_NAME", "proxy-db")
	dbSSLMode := getEnv("DB_SSLMODE", "disable")

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		dbHost,
		dbPort,
		dbUser,
		dbPassword,
		dbName,
		dbSSLMode,
	)

	// Connect to database
	database, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info), // Show SQL queries in console
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
	)

	if err != nil {
		log.Fatal("Failed to migrate database:", err)
		return err
	}

	log.Println("Database migration completed")

	return nil
}
