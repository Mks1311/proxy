package openrouter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/Mks1311/poolify/internal/http/handlers/apikey"
	"github.com/Mks1311/poolify/internal/provider"
)

const (
	openRouterAPIURL = "https://openrouter.ai/api/v1/chat/completions"
	maxRetries       = 3
	cooldownDuration = 60 * time.Second
	defaultModel     = "nex-agi/nex-n2-pro:free"
)

// Message represents a chat message in OpenAI-compatible format.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the request body sent to the OpenRouter API.
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

// ChatResponse represents the full response from OpenRouter.
type ChatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage *Usage    `json:"usage,omitempty"`
	Error *APIError `json:"error,omitempty"`
}

// Usage holds token usage info from the OpenRouter response.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// APIError represents an error returned by the OpenRouter API.
type APIError struct {
	Message string      `json:"message"`
	Type    string      `json:"type"`
	Code    interface{} `json:"code"` // OpenRouter may return int or string
}

// OpenRouterProvider implements the provider.Provider interface for OpenRouter.
type OpenRouterProvider struct{}

// New creates a new OpenRouterProvider.
func New() *OpenRouterProvider {
	return &OpenRouterProvider{}
}

func (o *OpenRouterProvider) Name() string {
	return "openrouter"
}

func (o *OpenRouterProvider) DefaultModel() string {
	return defaultModel
}

// setOpenRouterHeaders sets the common headers for OpenRouter requests.
func setOpenRouterHeaders(req *http.Request, apiKeyValue string) {
	req.Header.Set("Authorization", "Bearer "+apiKeyValue)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://poolify.app")
	req.Header.Set("X-Title", "Poolify")
}

// Execute performs the actual HTTP call to the OpenRouter API.
// Includes automatic retry with key rotation on 429 errors.
func (o *OpenRouterProvider) Execute(payload []byte, model string) provider.Result {
	// Parse the payload to extract messages
	var input struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(payload, &input); err != nil {
		return provider.Result{
			StatusCode:   500,
			ProviderName: "openrouter",
			ModelUsed:    model,
			Error:        fmt.Errorf("failed to parse payload: %w", err),
		}
	}

	// Build the OpenRouter-specific request
	reqBody := ChatRequest{
		Model:    model,
		Messages: input.Messages,
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return provider.Result{
			StatusCode:   500,
			ProviderName: "openrouter",
			ModelUsed:    model,
			Error:        fmt.Errorf("failed to marshal request: %w", err),
		}
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		// 1. Get an available API key from the pool
		orApiKey, keyID, ok := apikey.ConsumeAvailableKey("openrouter")
		if !ok {
			return provider.Result{
				StatusCode:      503,
				NoKeysAvailable: true,
				ProviderName:    "openrouter",
				ModelUsed:       model,
				Error:           fmt.Errorf("no available API key for openrouter"),
			}
		}

		// 2. Create HTTP request to OpenRouter
		req, err := http.NewRequest("POST", openRouterAPIURL, bytes.NewBuffer(jsonData))
		if err != nil {
			return provider.Result{
				StatusCode:   500,
				ProviderName: "openrouter",
				ModelUsed:    model,
				Error:        fmt.Errorf("failed to create request: %w", err),
			}
		}

		setOpenRouterHeaders(req, orApiKey)

		// 3. Execute the request
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return provider.Result{
				StatusCode:   500,
				ProviderName: "openrouter",
				ModelUsed:    model,
				Error:        fmt.Errorf("API request failed: %w", err),
			}
		}

		// 4. Read the response body
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return provider.Result{
				StatusCode:   500,
				ProviderName: "openrouter",
				ModelUsed:    model,
				Error:        fmt.Errorf("failed to read response: %w", err),
			}
		}

		// 5. Handle 429 — cooldown this key and retry with another
		if resp.StatusCode == 429 {
			log.Printf("Got 429 from OpenRouter with key %d, attempt %d/%d. Cooling down key and retrying...",
				keyID, attempt+1, maxRetries)
			apikey.CooldownKey(keyID, cooldownDuration)
			continue
		}

		// 6. If upstream returned a non-429 error status, pass it through
		if resp.StatusCode != 200 {
			return provider.Result{
				StatusCode:   resp.StatusCode,
				Body:         body,
				ProviderName: "openrouter",
				ModelUsed:    model,
				Error:        fmt.Errorf("upstream API error (status %d)", resp.StatusCode),
			}
		}

		// 7. Parse the response to extract token usage
		var chatResp ChatResponse
		if err := json.Unmarshal(body, &chatResp); err != nil {
			return provider.Result{
				StatusCode:   500,
				Body:         body,
				ProviderName: "openrouter",
				ModelUsed:    model,
				Error:        fmt.Errorf("failed to parse API response: %w", err),
			}
		}

		// 8. Check for API-level error
		if chatResp.Error != nil {
			return provider.Result{
				StatusCode:   500,
				Body:         body,
				ProviderName: "openrouter",
				ModelUsed:    model,
				Error:        fmt.Errorf("API error: %s", chatResp.Error.Message),
			}
		}

		// 9. Validate choices exist
		if len(chatResp.Choices) == 0 {
			return provider.Result{
				StatusCode:   500,
				Body:         body,
				ProviderName: "openrouter",
				ModelUsed:    model,
				Error:        fmt.Errorf("API returned no choices"),
			}
		}

		// 10. Build successful result with token usage
		result := provider.Result{
			StatusCode:   200,
			Body:         body,
			ProviderName: "openrouter",
			ModelUsed:    model,
		}

		if chatResp.Usage != nil {
			result.PromptTokens = chatResp.Usage.PromptTokens
			result.CompletionTokens = chatResp.Usage.CompletionTokens
			result.TotalTokens = chatResp.Usage.TotalTokens
		}

		return result
	}

	// All retries exhausted — signal chain to try next provider
	return provider.Result{
		StatusCode:      429,
		NoKeysAvailable: true,
		ProviderName:    "openrouter",
		ModelUsed:       model,
		Error:           fmt.Errorf("all OpenRouter API keys are rate-limited"),
	}
}

// ValidateKey makes a minimal API call to verify an OpenRouter API key works.
func (o *OpenRouterProvider) ValidateKey(apiKeyValue string) error {
	payload := map[string]interface{}{
		"model":      defaultModel,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 1,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal validation request: %w", err)
	}

	req, err := http.NewRequest("POST", openRouterAPIURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create validation request: %w", err)
	}

	setOpenRouterHeaders(req, apiKeyValue)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("validation request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return fmt.Errorf("invalid API key")
	}

	// Key is valid but rate-limited — still a valid key
	if resp.StatusCode == 429 {
		return nil
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("validation failed (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}
