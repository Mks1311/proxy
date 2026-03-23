package user

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Mks1311/poolify/internal/database"
	"github.com/Mks1311/poolify/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func Signup(c *gin.Context) {

	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	if err := c.BindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request body. Expected: {\"email\": \"your email\", \"password\": \"your password\"}"})
		return
	}

	if strings.TrimSpace(body.Email) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Email cannot be empty"})
		return
	}

	if strings.TrimSpace(body.Password) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Password cannot be empty"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), 10)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to hash password"})
		return
	}

	id := uuid.New().String()
	apiKey := "pk_" + uuid.New().String() // pk_ prefix for "poolify key"

	user := models.User{
		ID:           id,
		Email:        body.Email,
		PasswordHash: string(hash),
		APIKey:       &apiKey,
	}

	if err := database.DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user": gin.H{
			"id":          user.ID,
			"email":       user.Email,
			"api_key":     user.APIKey,
			"daily_limit": user.DailyLimit,
		},
	})
}

func Login(c *gin.Context) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	if c.Bind(&body) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read body"})
		return
	}

	// Validation
	if strings.TrimSpace(body.Email) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Email cannot be empty"})
		return
	}

	if strings.TrimSpace(body.Password) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Password cannot be empty"})
		return
	}

	var user models.User
	database.DB.First(&user, "email = ? ", body.Email)

	if user.ID == "0" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user"})
		return
	}

	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.Password))

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid email or password"})
		fmt.Printf("Login failed for email: %s, error: %v", body.Password, err)
		return
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": user.ID,
		"exp": time.Now().Add(time.Hour * 24 * 30).Unix(),
	})

	tokenString, err := token.SignedString([]byte(os.Getenv("JWT_SECRET")))

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to login",
		})
		return
	}

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("Authorization", tokenString, 3600*24*30, "", "", false, true)
	c.JSON(http.StatusOK, gin.H{
		"success": "ok",
		"user": gin.H{
			"id":          user.ID,
			"email":       user.Email,
			"api_key":     user.APIKey,
			"daily_limit": user.DailyLimit,
		},
	})

}

func Validate(c *gin.Context) {
	user, _ := c.Get("user")
	c.JSON(http.StatusOK, gin.H{
		"message": user,
	})
}

func Logout(c *gin.Context) {
	// Delete the cookie by setting same name but expired
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(
		"Authorization",
		"",
		-1, // Expire immediately
		"",
		"",
		false,
		true, // HttpOnly
	)

	c.JSON(http.StatusOK, gin.H{
		"message": "Logged out successfully",
	})
}

func GetUserByApiKey(apiKey string) (*models.User, error) {
	var user models.User
	result := database.DB.First(&user, "api_key = ?", apiKey)

	if result.Error != nil {
		return nil, result.Error
	}

	return &user, nil
}
