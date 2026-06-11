package openrouter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/Mks1311/poolify/internal/http/handlers/apikey"
	"github.com/Mks1311/poolify/internal/provider"
)

// StreamRequest is the request body for streaming OpenRouter API calls.
type StreamRequest struct {
	Model         string        `json:"model"`
	Messages      []Message     `json:"messages"`
	Stream        bool          `json:"stream"`
	StreamOptions *StreamOption `json:"stream_options,omitempty"`
}

// StreamOption controls streaming behavior.
type StreamOption struct {
	IncludeUsage bool `json:"include_usage"`
}

// OpenRouterStreamChunk represents a single SSE chunk from OpenRouter's streaming API.
type OpenRouterStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
			Role    string `json:"role"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage,omitempty"`
}

// ExecuteStream performs a streaming HTTP call to the OpenRouter API.
// Returns true if the provider handled the request, false if no keys were available.
// Does NOT close ch — the caller (chain) is responsible for that.
func (o *OpenRouterProvider) ExecuteStream(payload []byte, model string, ch chan<- provider.StreamChunk) bool {
	for attempt := 0; attempt < maxRetries; attempt++ {
		// 1. Get an available API key
		orApiKey, keyID, ok := apikey.ConsumeAvailableKey("openrouter")
		if !ok {
			return false // No keys available, let chain try next provider
		}

		// 2. Parse payload to extract messages
		var input struct {
			Messages []Message `json:"messages"`
		}
		if err := json.Unmarshal(payload, &input); err != nil {
			ch <- provider.StreamChunk{
				Error:        fmt.Errorf("failed to parse payload: %w", err),
				Done:         true,
				ProviderName: "openrouter",
				ModelUsed:    model,
			}
			return true
		}

		// 3. Build the streaming request
		streamReq := StreamRequest{
			Model:    model,
			Messages: input.Messages,
			Stream:   true,
			StreamOptions: &StreamOption{
				IncludeUsage: true,
			},
		}

		jsonData, err := json.Marshal(streamReq)
		if err != nil {
			ch <- provider.StreamChunk{
				Error:        fmt.Errorf("failed to marshal stream request: %w", err),
				Done:         true,
				ProviderName: "openrouter",
				ModelUsed:    model,
			}
			return true
		}

		// 4. Create HTTP request
		req, err := http.NewRequest("POST", openRouterAPIURL, bytes.NewBuffer(jsonData))
		if err != nil {
			ch <- provider.StreamChunk{
				Error:        fmt.Errorf("failed to create request: %w", err),
				Done:         true,
				ProviderName: "openrouter",
				ModelUsed:    model,
			}
			return true
		}

		setOpenRouterHeaders(req, orApiKey)

		// 5. Execute the request
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			ch <- provider.StreamChunk{
				Error:        fmt.Errorf("API request failed: %w", err),
				Done:         true,
				ProviderName: "openrouter",
				ModelUsed:    model,
			}
			return true
		}

		// 6. Handle 429 — cooldown this key and retry
		if resp.StatusCode == 429 {
			resp.Body.Close()
			log.Printf("Got 429 from OpenRouter (stream) with key %d, attempt %d/%d. Cooling down and retrying...",
				keyID, attempt+1, maxRetries)
			apikey.CooldownKey(keyID, cooldownDuration)
			continue // retry with a new key
		}

		// 7. Handle non-200 errors
		if resp.StatusCode != 200 {
			resp.Body.Close()
			ch <- provider.StreamChunk{
				Error:        fmt.Errorf("upstream API error (status %d)", resp.StatusCode),
				Done:         true,
				ProviderName: "openrouter",
				ModelUsed:    model,
			}
			return true
		}

		// 8. Read the SSE stream line by line
		scanner := bufio.NewScanner(resp.Body)
		var finalUsage *provider.TokenUsageInfo

		for scanner.Scan() {
			line := scanner.Text()

			// SSE lines start with "data: "
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")

			// Check for the [DONE] signal
			if data == "[DONE]" {
				ch <- provider.StreamChunk{
					Data:         "[DONE]",
					Done:         true,
					Usage:        finalUsage,
					ProviderName: "openrouter",
					ModelUsed:    model,
				}
				resp.Body.Close()
				return true
			}

			// Parse the chunk to extract content and usage
			var chunk OpenRouterStreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				// Forward unparseable chunks as-is
				ch <- provider.StreamChunk{
					Data:         data,
					ProviderName: "openrouter",
					ModelUsed:    model,
				}
				continue
			}

			// Extract token usage from the final chunk
			if chunk.Usage != nil {
				finalUsage = &provider.TokenUsageInfo{
					PromptTokens:     chunk.Usage.PromptTokens,
					CompletionTokens: chunk.Usage.CompletionTokens,
					TotalTokens:      chunk.Usage.TotalTokens,
				}
			}

			// Extract content delta
			content := ""
			if len(chunk.Choices) > 0 {
				content = chunk.Choices[0].Delta.Content
			}

			// Only send chunks that have content
			if content != "" {
				ch <- provider.StreamChunk{
					Data:         data,
					ProviderName: "openrouter",
					ModelUsed:    model,
				}
			}
		}

		resp.Body.Close()

		if err := scanner.Err(); err != nil {
			ch <- provider.StreamChunk{
				Error:        fmt.Errorf("error reading stream: %w", err),
				Done:         true,
				ProviderName: "openrouter",
				ModelUsed:    model,
			}
			return true
		}

		// If we got here without [DONE], send final signal
		ch <- provider.StreamChunk{
			Done:         true,
			Usage:        finalUsage,
			ProviderName: "openrouter",
			ModelUsed:    model,
		}
		return true
	}

	// All retries exhausted — let chain try next provider
	return false
}
