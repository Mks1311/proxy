package gropqproxy

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/Mks1311/poolify/internal/cache"
	"github.com/Mks1311/poolify/internal/database"
	"github.com/Mks1311/poolify/internal/models"
	"github.com/Mks1311/poolify/internal/ratelimit"
	"github.com/Mks1311/poolify/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// Sched is the global scheduler reference, set by main.go during init.
var Sched *scheduler.Scheduler

func GroqProxy(c *gin.Context) {
	// 1. Parse request body
	var input struct {
		Message string `json:"message"`
		Stream  bool   `json:"stream"`
	}

	if err := c.BindJSON(&input); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request body. Expected: {\"message\": \"your text\", \"stream\": false}"})
		return
	}

	// 2. Validate input
	if input.Message == "" {
		c.JSON(400, gin.H{"error": "Message cannot be empty"})
		return
	}

	// 3. Get user from context
	userInterface, exists := c.Get("user")
	if !exists {
		c.JSON(500, gin.H{"error": "User not found in context"})
		return
	}
	user := userInterface.(*models.User)

	// 4. Build the upstream request payload
	model := "llama-3.3-70b-versatile"
	reqBody := scheduler.GroqChatRequest{
		Model: model,
		Messages: []scheduler.GroqMessage{
			{Role: "user", Content: input.Message},
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to marshal request"})
		return
	}

	// Branch: streaming vs non-streaming
	if input.Stream {
		handleStreamingRequest(c, user, model, jsonData)
	} else {
		handleNonStreamingRequest(c, user, model, jsonData)
	}
}

// handleNonStreamingRequest handles the standard JSON request/response flow with caching.
func handleNonStreamingRequest(c *gin.Context, user *models.User, model string, jsonData []byte) {
	// 1. Check cache first
	cacheKey := cache.GenerateKey(jsonData)
	if cachedBody, hit := cache.Get(cacheKey); hit {
		// Cache hit — return cached response without deducting tokens
		var chatResp scheduler.GroqChatResponse
		if err := json.Unmarshal(cachedBody, &chatResp); err == nil && len(chatResp.Choices) > 0 {
			c.JSON(http.StatusOK, gin.H{
				"message": chatResp.Choices[0].Message.Content,
				"cached":  true,
				"usage": gin.H{
					"prompt_tokens":     0,
					"completion_tokens": 0,
					"total_tokens":      0,
				},
			})
			return
		}
	}

	// 2. Submit to the fair-queuing scheduler
	job := &scheduler.Job{
		UserID:   user.ID,
		Service:  "groq",
		Model:    model,
		Payload:  jsonData,
		Stream:   false,
		Response: make(chan scheduler.JobResult, 1),
	}

	Sched.Submit(job)

	// 3. Block until the scheduler worker processes this job
	result := <-job.Response

	// 4. Handle errors from the worker
	if result.Error != nil {
		if result.Body != nil {
			c.JSON(result.StatusCode, gin.H{
				"error":   result.Error.Error(),
				"details": string(result.Body),
			})
		} else {
			c.JSON(result.StatusCode, gin.H{"error": result.Error.Error()})
		}
		return
	}

	// 5. Cache the successful response
	if result.Body != nil {
		cache.Set(cacheKey, result.Body, cache.DefaultTTL)
	}

	// 6. Deduct tokens from the user's budget (post-response)
	if result.TotalTokens > 0 {
		deductAndLogTokens(c, user, model, result.PromptTokens, result.CompletionTokens, result.TotalTokens)
	}

	// 7. Parse the response body to extract the message
	var chatResp scheduler.GroqChatResponse
	if err := json.Unmarshal(result.Body, &chatResp); err != nil {
		c.JSON(500, gin.H{
			"error":        "Failed to parse API response",
			"raw_response": string(result.Body),
		})
		return
	}

	if len(chatResp.Choices) == 0 {
		c.JSON(500, gin.H{
			"error":        "API returned no choices",
			"raw_response": string(result.Body),
		})
		return
	}

	// 8. Return successful response with token usage info
	c.JSON(http.StatusOK, gin.H{
		"message": chatResp.Choices[0].Message.Content,
		"cached":  false,
		"usage": gin.H{
			"prompt_tokens":     result.PromptTokens,
			"completion_tokens": result.CompletionTokens,
			"total_tokens":      result.TotalTokens,
		},
	})
}

// handleStreamingRequest handles SSE streaming responses.
func handleStreamingRequest(c *gin.Context, user *models.User, model string, jsonData []byte) {
	// 1. Submit a streaming job to the scheduler
	job := &scheduler.Job{
		UserID:     user.ID,
		Service:    "groq",
		Model:      model,
		Payload:    jsonData,
		Stream:     true,
		StreamChan: make(chan scheduler.StreamChunk, 100),
	}

	Sched.Submit(job)

	// 2. Set SSE headers
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Writer.WriteHeader(http.StatusOK)

	// Get the flusher for real-time streaming
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(500, gin.H{"error": "Streaming not supported"})
		return
	}

	// 3. Read chunks from the scheduler and forward them as SSE events
	var finalUsage *scheduler.TokenUsageInfo

	for chunk := range job.StreamChan {
		// Handle errors
		if chunk.Error != nil {
			fmt.Fprintf(c.Writer, "data: {\"error\": \"%s\"}\n\n", chunk.Error.Error())
			flusher.Flush()
			break
		}

		// Capture usage from the final chunk
		if chunk.Usage != nil {
			finalUsage = chunk.Usage
		}

		// Forward SSE data
		if chunk.Done {
			// Send usage info before [DONE] if we have it
			if finalUsage != nil {
				usageJSON, _ := json.Marshal(gin.H{
					"usage": gin.H{
						"prompt_tokens":     finalUsage.PromptTokens,
						"completion_tokens": finalUsage.CompletionTokens,
						"total_tokens":      finalUsage.TotalTokens,
					},
				})
				fmt.Fprintf(c.Writer, "data: %s\n\n", string(usageJSON))
				flusher.Flush()
			}

			fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
			flusher.Flush()
		} else if chunk.Data != "" {
			fmt.Fprintf(c.Writer, "data: %s\n\n", chunk.Data)
			flusher.Flush()
		}
	}

	// 4. Deduct tokens and log usage after stream completes
	if finalUsage != nil && finalUsage.TotalTokens > 0 {
		deductAndLogTokens(c, user, model, finalUsage.PromptTokens, finalUsage.CompletionTokens, finalUsage.TotalTokens)
	}
}

// deductAndLogTokens handles the post-response token deduction from Redis
// and writes a TokenUsage record to Postgres.
func deductAndLogTokens(c *gin.Context, user *models.User, model string, promptTokens, completionTokens, totalTokens int) {
	// Deduct from Redis budget
	limiterInterface, exists := c.Get("limiter")
	if exists {
		limiter := limiterInterface.(*ratelimit.TokenBucket)
		_, err := limiter.DeductTokens(user.ID, totalTokens, user.DailyLimit)
		if err != nil {
			log.Printf("Warning: failed to deduct tokens for user %s: %v", user.ID, err)
		}
	}

	// Log to Postgres
	tokenUsage := models.TokenUsage{
		UserID:           user.ID,
		Service:          "groq",
		Model:            model,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
	}
	if err := database.DB.Create(&tokenUsage).Error; err != nil {
		log.Printf("Warning: failed to log token usage: %v", err)
	}
}
