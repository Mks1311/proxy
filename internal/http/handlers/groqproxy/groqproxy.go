package gropqproxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *APIError `json:"error,omitempty"`
}

type APIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

func GroqProxy(c *gin.Context) {
	// 1. Get message from request body
	var input struct {
		Message string `json:"message"`
	}

	if err := c.BindJSON(&input); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request body. Expected: {\"message\": \"your text\"}"})
		return
	}

	// 2. Validate input
	if input.Message == "" {
		c.JSON(400, gin.H{"error": "Message cannot be empty"})
		return
	}

	// 3. Get API key
	groqApiKey := os.Getenv("GROK_1")
	if groqApiKey == "" {
		c.JSON(500, gin.H{"error": "GROK_1 not set in environment"})
		return
	}

	// 4. Prepare request to Groq
	reqBody := ChatRequest{
		Model: "llama-3.3-70b-versatile",
		Messages: []Message{
			{Role: "user", Content: input.Message},
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to marshal request"})
		return
	}

	// 5. Create HTTP request
	req, err := http.NewRequest("POST",
		"https://api.groq.com/openai/v1/chat/completions",
		bytes.NewBuffer(jsonData))

	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to create request"})
		return
	}

	req.Header.Set("Authorization", "Bearer "+groqApiKey)
	req.Header.Set("Content-Type", "application/json")

	// 6. Make the API call
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(500, gin.H{"error": fmt.Sprintf("API request failed: %v", err)})
		return
	}
	defer resp.Body.Close()

	// 7. Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to read response"})
		return
	}

	// 8. Log raw response for debugging
	fmt.Printf("Groq API Response Status: %d\n", resp.StatusCode)
	fmt.Printf("Groq API Response Body: %s\n", string(body))

	// 9. Check HTTP status
	if resp.StatusCode != 200 {
		c.JSON(resp.StatusCode, gin.H{
			"error":   "Upstream API error",
			"details": string(body),
		})
		return
	}

	// 10. Parse response
	var chatResp ChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		c.JSON(500, gin.H{
			"error":        "Failed to parse API response",
			"raw_response": string(body),
		})
		return
	}

	// 11. Check for API error
	if chatResp.Error != nil {
		c.JSON(500, gin.H{
			"error":   "API returned error",
			"message": chatResp.Error.Message,
			"type":    chatResp.Error.Type,
		})
		return
	}

	// 12. Validate choices exist
	if len(chatResp.Choices) == 0 {
		c.JSON(500, gin.H{
			"error":        "API returned no choices",
			"raw_response": string(body),
		})
		return
	}

	// 13. Return successful response
	c.JSON(http.StatusOK, gin.H{
		"message": chatResp.Choices[0].Message.Content,
	})
}
