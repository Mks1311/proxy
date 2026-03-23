package middleware

import (
	"strings"

	"github.com/Mks1311/poolify/internal/http/handlers/user"
	"github.com/gin-gonic/gin"
)

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get API key from header
		apiKey := c.GetHeader("X-API-Key")

		// Also check Authorization header (Bearer token format)
		if apiKey == "" {
			authHeader := c.GetHeader("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				apiKey = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}

		if apiKey == "" {
			c.JSON(401, gin.H{
				"error": "Missing API key. Include 'X-API-Key' header or 'Authorization: Bearer <key>'",
			})
			c.Abort()
			return
		}

		// Validate API key
		user, err := user.GetUserByApiKey(apiKey)
		if err != nil {
			c.JSON(401, gin.H{
				"error": "Invalid API key",
			})
			c.Abort()
			return
		}

		// Store user in context for later use
		c.Set("user", user)
		c.Set("user_id", user.ID)

		c.Next()
	}
}
