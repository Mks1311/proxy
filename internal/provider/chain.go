package provider

import (
	"fmt"
	"log"
)

// Chain holds registered providers and executes requests with fallback.
type Chain struct {
	providers    map[string]Provider
	defaultOrder []string // default provider priority (registration order)
}

// NewChain creates a new empty provider chain.
func NewChain() *Chain {
	return &Chain{
		providers: make(map[string]Provider),
	}
}

// Register adds a provider to the chain.
// Providers are tried in registration order by default.
func (c *Chain) Register(p Provider) {
	c.providers[p.Name()] = p
	c.defaultOrder = append(c.defaultOrder, p.Name())
}

// resolveOrder determines the provider order based on user priority.
// If priority is empty or nil, uses default registration order.
// Any providers not in the user's list are appended at the end.
func (c *Chain) resolveOrder(priority []string) []string {
	if len(priority) == 0 {
		return c.defaultOrder
	}

	order := make([]string, 0, len(c.providers))
	seen := make(map[string]bool)

	// Add user-specified providers first (filter out invalid names)
	for _, name := range priority {
		if _, ok := c.providers[name]; ok && !seen[name] {
			order = append(order, name)
			seen[name] = true
		}
	}

	// Append any remaining providers not in the user's priority list
	for _, name := range c.defaultOrder {
		if !seen[name] {
			order = append(order, name)
		}
	}

	return order
}

// Execute tries providers in order until one succeeds or all are exhausted.
func (c *Chain) Execute(payload []byte, model string, priority []string) Result {
	order := c.resolveOrder(priority)

	for _, name := range order {
		p := c.providers[name]

		useModel := model
		if useModel == "" {
			useModel = p.DefaultModel()
		}

		log.Printf("Trying provider: %s (model: %s)", name, useModel)
		result := p.Execute(payload, useModel)

		if result.NoKeysAvailable {
			log.Printf("Provider %s: no keys available, trying next provider...", name)
			continue
		}

		return result
	}

	return Result{
		StatusCode: 503,
		Error:      fmt.Errorf("all providers exhausted, no available API keys"),
	}
}

// ExecuteStream tries providers in order for streaming until one handles the request.
// The chain closes ch when done — providers must NOT close it.
func (c *Chain) ExecuteStream(payload []byte, model string, priority []string, ch chan<- StreamChunk) {
	defer close(ch)

	order := c.resolveOrder(priority)

	for _, name := range order {
		p := c.providers[name]

		useModel := model
		if useModel == "" {
			useModel = p.DefaultModel()
		}

		log.Printf("Trying provider (stream): %s (model: %s)", name, useModel)

		// ExecuteStream returns true if the provider handled the request,
		// false if no keys were available.
		if p.ExecuteStream(payload, useModel, ch) {
			return
		}

		log.Printf("Provider %s: no keys available for streaming, trying next...", name)
	}

	// All providers exhausted
	ch <- StreamChunk{
		Error: fmt.Errorf("all providers exhausted, no available API keys"),
		Done:  true,
	}
}

// ValidateKey validates an API key for the specified service provider.
func (c *Chain) ValidateKey(service, apiKey string) error {
	p, ok := c.providers[service]
	if !ok {
		return fmt.Errorf("unknown service provider: %s", service)
	}
	return p.ValidateKey(apiKey)
}

// GetProvider returns a registered provider by name.
func (c *Chain) GetProvider(name string) (Provider, bool) {
	p, ok := c.providers[name]
	return p, ok
}
