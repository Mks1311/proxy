package main

import (
	"log"
	"net/http"

	"github.com/Mks1311/poolify/internal/database"
	"github.com/Mks1311/poolify/internal/http/handlers/apikey"
	gropqproxy "github.com/Mks1311/poolify/internal/http/handlers/groqproxy"
	"github.com/Mks1311/poolify/internal/http/handlers/user"
	"github.com/Mks1311/poolify/internal/http/middleware"
	"github.com/Mks1311/poolify/internal/utils"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/robfig/cron"
)

func RunCron() {
	c := cron.New()

	err := c.AddFunc("@daily", utils.ResetApiUsageDaily)
	if err != nil {
		log.Fatal("Failed to schedule cron:", err)
	}

	c.Start()
	log.Println("Cron started...")
}

func main() {

	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	//setup database connection
	err = database.ConnectPostgres()
	if err != nil {
		log.Fatal("Error connecting to database:", err)
	}

	err = database.InitRedis()
	if err != nil {
		log.Fatal("Error connecting to Redis:", err)
	}

	// Create a Gin router with default middleware (logger and recovery)
	r := gin.Default()

	// Configure CORS
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Content-Length", "Accept-Encoding", "X-CSRF-Token", "Authorization", "accept", "origin", "Cache-Control", "X-Requested-With"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * 60 * 60, // 12 hours
	}))

	// Define a simple GET endpoint
	r.GET("/ping", func(c *gin.Context) {
		// Return JSON response
		c.JSON(http.StatusOK, gin.H{
			"message": "pong",
		})
	})

	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":  "ok",
			"message": "server is running",
		})
	})

	userRoute := r.Group("/user")
	{
		userRoute.POST("/signup", user.Signup)
		userRoute.POST("/login", user.Login)
		userRoute.GET("/validate", user.Validate)
		userRoute.POST("/logout", user.Logout)
	}

	// proxy endpoint group
	proxyRoute := r.Group("/proxy")
	proxyRoute.Use(middleware.AuthMiddleware())
	proxyRoute.Use(middleware.RateLimitMiddleware())
	{
		// groqai proxy endpoint
		proxyRoute.POST("/groqai", gropqproxy.GroqProxy)

	}

	apiKeyRoute := r.Group("/key")
	apiKeyRoute.Use(middleware.AuthMiddleware())
	{
		apiKeyRoute.POST("/add", apikey.AddApiKey)
	}

	// running cron job
	RunCron()

	// Start server on port 8080 (default)
	// Server will listen on 0.0.0.0:8080 (localhost:8080 on Windows)
	r.Run()
}
