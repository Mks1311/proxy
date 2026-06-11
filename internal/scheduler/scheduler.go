package scheduler

import (
	"log"
	"sync"

	"github.com/Mks1311/poolify/internal/provider"
)

// Job represents a single proxy request submitted by a user.
type Job struct {
	UserID           string
	Service          string                   // kept for logging/testing
	Model            string                   // if empty, each provider uses its default
	Payload          []byte                   // The raw JSON body (messages only, no model)
	Stream           bool                     // If true, use StreamChan instead of Response
	ProviderPriority []string                 // User-specified provider order, e.g. ["openrouter", "groq"]
	Response         chan provider.Result      // Used for non-streaming jobs
	StreamChan       chan provider.StreamChunk // Used for streaming jobs
}

// Scheduler implements fair queuing with per-user round-robin dispatching.
type Scheduler struct {
	chain      *provider.Chain
	submitChan chan *Job
	mu         sync.Mutex
	userQueues map[string][]*Job
	roundRobin []string // ordered list of user IDs for round-robin
	rrIndex    int      // current position in the round-robin
}

// NewScheduler creates a scheduler and starts the dispatcher + worker pool.
func NewScheduler(workerCount int, chain *provider.Chain) *Scheduler {
	s := &Scheduler{
		chain:      chain,
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

// worker processes jobs from the dispatch channel using the provider chain.
func (s *Scheduler) worker(id int, dispatchChan <-chan *Job) {
	for job := range dispatchChan {
		if job.Stream {
			// Streaming job: chain writes chunks to StreamChan and closes it when done
			s.chain.ExecuteStream(job.Payload, job.Model, job.ProviderPriority, job.StreamChan)
		} else {
			// Non-streaming job: chain returns a result
			result := s.chain.Execute(job.Payload, job.Model, job.ProviderPriority)
			job.Response <- result
		}
	}
}
