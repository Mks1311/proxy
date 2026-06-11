package provider

// Provider defines the interface that all AI service providers must implement.
type Provider interface {
	// Name returns the provider identifier (e.g., "groq", "openrouter").
	Name() string

	// DefaultModel returns the default free model for this provider.
	DefaultModel() string

	// Execute performs a non-streaming API call.
	// Returns Result with NoKeysAvailable=true if the chain should try the next provider.
	Execute(payload []byte, model string) Result

	// ExecuteStream performs a streaming API call, writing chunks to ch.
	// Returns true if the provider handled the request (success or upstream error).
	// Returns false if no keys were available (chain should try next provider).
	// The caller (chain) is responsible for closing ch.
	ExecuteStream(payload []byte, model string, ch chan<- StreamChunk) bool

	// ValidateKey makes a minimal API call to verify that the given key is valid.
	ValidateKey(apiKey string) error
}

// Result is the outcome of a non-streaming provider call.
type Result struct {
	StatusCode       int
	Body             []byte
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Error            error
	NoKeysAvailable  bool   // Signals the chain to try the next provider
	ProviderName     string // Which provider handled this request
	ModelUsed        string // Which model was used
}

// StreamChunk is sent incrementally to the HTTP handler during streaming.
type StreamChunk struct {
	Data         string          // The SSE data payload (JSON chunk or "[DONE]")
	Done         bool            // True for the final signal
	Usage        *TokenUsageInfo // Populated only on the final chunk
	Error        error
	ProviderName string // Which provider is streaming
	ModelUsed    string // Which model is being used
}

// TokenUsageInfo carries token usage from the final streaming chunk.
type TokenUsageInfo struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// SupportedProviders returns the list of valid provider names.
func SupportedProviders() []string {
	return []string{"groq", "openrouter"}
}

// IsValidProvider checks if the given service name is a supported provider.
func IsValidProvider(service string) bool {
	for _, p := range SupportedProviders() {
		if p == service {
			return true
		}
	}
	return false
}
