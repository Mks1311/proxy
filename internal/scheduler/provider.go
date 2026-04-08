package scheduler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/Mks1311/poolify/internal/http/handlers/apikey"
)

const maxRetries = 3
const cooldownDuration = 60 * time.Second

// GroqChatRequest is the request body sent to the Groq API.
type GroqChatRequest struct {
	Model    string        `json:"model"`
	Messages []GroqMessage `json:"messages"`
}

type GroqMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// GroqChatResponse represents the full response from Groq.
type GroqChatResponse struct {
	Choices []struct {
		Message GroqMessage `json:"message"`
	} `json:"choices"`
	Usage *GroqUsage `json:"usage,omitempty"`
	Error *GroqError `json:"error,omitempty"`
}

type GroqUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type GroqError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// ExecuteGroqRequest performs the actual HTTP call to the Groq API.
// Includes automatic retry with key rotation on 429 errors.
func ExecuteGroqRequest(payload []byte, model string) JobResult {
	for attempt := 0; attempt < maxRetries; attempt++ {
		// 1. Get an available API key from the pool
		groqApiKey, keyID, ok := apikey.ConsumeAvailableKey("groq")
		if !ok {
			return JobResult{
				StatusCode: 503,
				Error:      fmt.Errorf("no available API key for groq"),
			}
		}

		// 2. Create HTTP request to Groq
		req, err := http.NewRequest("POST",
			"https://api.groq.com/openai/v1/chat/completions",
			bytes.NewBuffer(payload))
		if err != nil {
			return JobResult{
				StatusCode: 500,
				Error:      fmt.Errorf("failed to create request: %w", err),
			}
		}

		req.Header.Set("Authorization", "Bearer "+groqApiKey)
		req.Header.Set("Content-Type", "application/json")

		// 3. Execute the request
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return JobResult{
				StatusCode: 500,
				Error:      fmt.Errorf("API request failed: %w", err),
			}
		}

		// 4. Read the response body
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return JobResult{
				StatusCode: 500,
				Error:      fmt.Errorf("failed to read response: %w", err),
			}
		}

		// 5. Handle 429 — cooldown this key and retry with another
		if resp.StatusCode == 429 {
			log.Printf("Got 429 from Groq with key %d, attempt %d/%d. Cooling down key and retrying...",
				keyID, attempt+1, maxRetries)
			apikey.CooldownKey(keyID, cooldownDuration)
			continue // retry with a new key
		}

		// 6. If upstream returned a non-429 error status, pass it through
		if resp.StatusCode != 200 {
			return JobResult{
				StatusCode: resp.StatusCode,
				Body:       body,
				Error:      fmt.Errorf("upstream API error (status %d)", resp.StatusCode),
			}
		}

		// 7. Parse the response to extract token usage
		var chatResp GroqChatResponse
		if err := json.Unmarshal(body, &chatResp); err != nil {
			return JobResult{
				StatusCode: 500,
				Body:       body,
				Error:      fmt.Errorf("failed to parse API response: %w", err),
			}
		}

		// 8. Check for API-level error
		if chatResp.Error != nil {
			return JobResult{
				StatusCode: 500,
				Body:       body,
				Error:      fmt.Errorf("API error: %s", chatResp.Error.Message),
			}
		}

		// 9. Validate choices exist
		if len(chatResp.Choices) == 0 {
			return JobResult{
				StatusCode: 500,
				Body:       body,
				Error:      fmt.Errorf("API returned no choices"),
			}
		}

		// 10. Build successful result with token usage
		result := JobResult{
			StatusCode: 200,
			Body:       body,
		}

		if chatResp.Usage != nil {
			result.PromptTokens = chatResp.Usage.PromptTokens
			result.CompletionTokens = chatResp.Usage.CompletionTokens
			result.TotalTokens = chatResp.Usage.TotalTokens
		}

		return result
	}

	// All retries exhausted
	return JobResult{
		StatusCode: 429,
		Error:      fmt.Errorf("all API keys are rate-limited, please try again later"),
	}
}
