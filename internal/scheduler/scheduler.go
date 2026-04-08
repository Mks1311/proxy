package scheduler

import (
	"fmt"
	"log"
	"sync"
)

// Job represents a single proxy request submitted by a user.
type Job struct {
	UserID     string
	Service    string
	Model      string
	Payload    []byte           // The raw JSON body to send to the upstream provider
	Stream     bool             // If true, use StreamChan instead of Response
	Response   chan JobResult    // Used for non-streaming jobs
	StreamChan chan StreamChunk  // Used for streaming jobs
}

// JobResult is sent back to the HTTP handler after the worker finishes (non-streaming).
type JobResult struct {
	StatusCode       int
	Body             []byte
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Error            error
}

// StreamChunk is sent incrementally to the HTTP handler during streaming.
type StreamChunk struct {
	Data  string         // The SSE data payload (JSON chunk or "[DONE]")
	Done  bool           // True for the final signal
	Usage *TokenUsageInfo // Populated only on the final chunk
	Error error
}

// TokenUsageInfo carries token usage from the final streaming chunk.
type TokenUsageInfo struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Scheduler implements fair queuing with per-user round-robin dispatching.
type Scheduler struct {
	submitChan chan *Job
	mu         sync.Mutex
	userQueues map[string][]*Job
	roundRobin []string // ordered list of user IDs for round-robin
	rrIndex    int      // current position in the round-robin
}

// NewScheduler creates a scheduler and starts the dispatcher + worker pool.
func NewScheduler(workerCount int) *Scheduler {
	s := &Scheduler{
		submitChan: make(chan *Job, 1000),
		userQueues: make(map[string][]*Job),
	}

	// dispatchChan feeds the worker pool
	dispatchChan := make(chan *Job, workerCount*2)

	// Start the dispatcher goroutine
	go s.dispatcher(dispatchChan)

	// Start the worker pool
	for i := 0; i < workerCount; i++ {
		go s.worker(i, dispatchChan)
	}

	log.Printf("Scheduler started with %d workers", workerCount)
	return s
}

// Submit adds a job to the scheduler. The caller should block on job.Response or job.StreamChan.
func (s *Scheduler) Submit(job *Job) {
	s.submitChan <- job
}

// dispatcher receives jobs from submitChan, queues them per-user,
// and dispatches them to workers in round-robin order.
func (s *Scheduler) dispatcher(dispatchChan chan<- *Job) {
	// pendingSignal is used to wake the drain loop when new jobs arrive
	pendingSignal := make(chan struct{}, 1)

	// Intake goroutine: receives from submitChan and adds to per-user queues
	go func() {
		for job := range s.submitChan {
			s.mu.Lock()

			_, exists := s.userQueues[job.UserID]
			if !exists {
				// New user — add them to the round-robin rotation
				s.roundRobin = append(s.roundRobin, job.UserID)
			}
			s.userQueues[job.UserID] = append(s.userQueues[job.UserID], job)

			s.mu.Unlock()

			// Signal that there are jobs to drain
			select {
			case pendingSignal <- struct{}{}:
			default:
			}
		}
	}()

	// Drain loop: picks jobs from user queues in round-robin order
	for range pendingSignal {
		s.drainQueues(dispatchChan, pendingSignal)
	}
}

// drainQueues iterates through user queues in round-robin and dispatches
// one job per user per cycle until all queues are empty.
func (s *Scheduler) drainQueues(dispatchChan chan<- *Job, pendingSignal chan struct{}) {
	for {
		s.mu.Lock()

		if len(s.roundRobin) == 0 {
			s.mu.Unlock()
			return
		}

		// Find the next user with a non-empty queue
		startIdx := s.rrIndex
		found := false
		var job *Job

		for i := 0; i < len(s.roundRobin); i++ {
			idx := (startIdx + i) % len(s.roundRobin)
			userID := s.roundRobin[idx]

			queue := s.userQueues[userID]
			if len(queue) > 0 {
				// Pop the first job from this user's queue
				job = queue[0]
				s.userQueues[userID] = queue[1:]

				// Clean up empty queues
				if len(s.userQueues[userID]) == 0 {
					delete(s.userQueues, userID)
					// Remove from round-robin slice
					s.roundRobin = append(s.roundRobin[:idx], s.roundRobin[idx+1:]...)
					if len(s.roundRobin) > 0 {
						s.rrIndex = idx % len(s.roundRobin)
					} else {
						s.rrIndex = 0
					}
				} else {
					// Move to the next user for the next iteration
					s.rrIndex = (idx + 1) % len(s.roundRobin)
				}

				found = true
				break
			}
		}

		s.mu.Unlock()

		if !found {
			return
		}

		// Send to worker pool (this may block if all workers are busy,
		// which provides natural backpressure)
		dispatchChan <- job

		// Re-signal so we continue draining
		select {
		case pendingSignal <- struct{}{}:
		default:
		}
	}
}

// worker processes jobs from the dispatch channel.
func (s *Scheduler) worker(id int, dispatchChan <-chan *Job) {
	for job := range dispatchChan {
		if job.Stream {
			// Streaming job: worker writes chunks to StreamChan, then closes it
			executeStreamingJob(job)
		} else {
			// Non-streaming job: worker sends a single result
			result := executeJob(job)
			job.Response <- result
		}
	}
}

// executeJob runs the actual upstream API call based on the service type (non-streaming).
func executeJob(job *Job) JobResult {
	switch job.Service {
	case "groq":
		return ExecuteGroqRequest(job.Payload, job.Model)
	default:
		return JobResult{
			StatusCode: 500,
			Error:      fmt.Errorf("unsupported service: %s", job.Service),
		}
	}
}

// executeStreamingJob runs the streaming upstream API call (writes chunks to StreamChan).
func executeStreamingJob(job *Job) {
	switch job.Service {
	case "groq":
		StreamGroqRequest(job.Payload, job.Model, job.StreamChan)
	default:
		job.StreamChan <- StreamChunk{
			Error: fmt.Errorf("unsupported service for streaming: %s", job.Service),
			Done:  true,
		}
		close(job.StreamChan)
	}
}
