package scheduler

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/Mks1311/poolify/internal/http/handlers/apikey"
)

// GroqStreamRequest is the request body for streaming Groq API calls.
type GroqStreamRequest struct {
	Model         string            `json:"model"`
	Messages      []GroqMessage     `json:"messages"`
	Stream        bool              `json:"stream"`
	StreamOptions *GroqStreamOption `json:"stream_options,omitempty"`
}

type GroqStreamOption struct {
	IncludeUsage bool `json:"include_usage"`
}

// GroqStreamChunk represents a single SSE chunk from Groq's streaming API.
type GroqStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
			Role    string `json:"role"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *GroqUsage `json:"usage,omitempty"`
}

// StreamGroqRequest performs a streaming HTTP call to the Groq API.
// It reads SSE chunks from the response and forwards them to streamChan.
// Includes automatic retry with key rotation on 429 errors.
func StreamGroqRequest(payload []byte, model string, streamChan chan<- StreamChunk) {
	defer close(streamChan)

	for attempt := 0; attempt < maxRetries; attempt++ {
		// 1. Get an available API key
		groqApiKey, keyID, ok := apikey.ConsumeAvailableKey("groq")
		if !ok {
			streamChan <- StreamChunk{
				Error: fmt.Errorf("no available API key for groq"),
				Done:  true,
			}
			return
		}

		// 2. Build the streaming request payload
		// We need to inject "stream": true and "stream_options" into the payload
		var originalReq GroqChatRequest
		if err := json.Unmarshal(payload, &originalReq); err != nil {
			streamChan <- StreamChunk{
				Error: fmt.Errorf("failed to parse payload: %w", err),
				Done:  true,
			}
			return
		}

		streamReq := GroqStreamRequest{
			Model:    originalReq.Model,
			Messages: originalReq.Messages,
			Stream:   true,
			StreamOptions: &GroqStreamOption{
				IncludeUsage: true, // Ask Groq to include usage in the final chunk
			},
		}

		jsonData, err := json.Marshal(streamReq)
		if err != nil {
			streamChan <- StreamChunk{
				Error: fmt.Errorf("failed to marshal stream request: %w", err),
				Done:  true,
			}
			return
		}

		// 3. Create HTTP request
		req, err := http.NewRequest("POST",
			"https://api.groq.com/openai/v1/chat/completions",
			bytes.NewBuffer(jsonData))
		if err != nil {
			streamChan <- StreamChunk{
				Error: fmt.Errorf("failed to create request: %w", err),
				Done:  true,
			}
			return
		}

		req.Header.Set("Authorization", "Bearer "+groqApiKey)
		req.Header.Set("Content-Type", "application/json")

		// 4. Execute the request
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			streamChan <- StreamChunk{
				Error: fmt.Errorf("API request failed: %w", err),
				Done:  true,
			}
			return
		}

		// 5. Handle 429 — cooldown this key and retry
		if resp.StatusCode == 429 {
			resp.Body.Close()
			log.Printf("Got 429 from Groq (stream) with key %d, attempt %d/%d. Cooling down and retrying...",
				keyID, attempt+1, maxRetries)
			apikey.CooldownKey(keyID, cooldownDuration)
			continue // retry with a new key
		}

		// 6. Handle non-200 errors
		if resp.StatusCode != 200 {
			resp.Body.Close()
			streamChan <- StreamChunk{
				Error: fmt.Errorf("upstream API error (status %d)", resp.StatusCode),
				Done:  true,
			}
			return
		}

		// 7. Read the SSE stream line by line
		scanner := bufio.NewScanner(resp.Body)
		var finalUsage *TokenUsageInfo

		for scanner.Scan() {
			line := scanner.Text()

			// SSE lines start with "data: "
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")

			// Check for the [DONE] signal
			if data == "[DONE]" {
				streamChan <- StreamChunk{
					Data:  "[DONE]",
					Done:  true,
					Usage: finalUsage,
				}
				resp.Body.Close()
				return
			}

			// Parse the chunk to extract content and usage
			var chunk GroqStreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				// Forward unparseable chunks as-is
				streamChan <- StreamChunk{Data: data}
				continue
			}

			// Extract token usage from the final chunk (Groq sends this when include_usage = true)
			if chunk.Usage != nil {
				finalUsage = &TokenUsageInfo{
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
				streamChan <- StreamChunk{
					Data: data,
				}
			}
		}

		resp.Body.Close()

		if err := scanner.Err(); err != nil {
			streamChan <- StreamChunk{
				Error: fmt.Errorf("error reading stream: %w", err),
				Done:  true,
			}
			return
		}

		// If we got here without [DONE], send final signal
		streamChan <- StreamChunk{
			Done:  true,
			Usage: finalUsage,
		}
		return
	}

	// All retries exhausted
	streamChan <- StreamChunk{
		Error: fmt.Errorf("all API keys are rate-limited, please try again later"),
		Done:  true,
	}
}
