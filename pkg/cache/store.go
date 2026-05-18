package cache

import (
	"sync"
	"time"
)

// LifecycleState represents the current state of EC2 lifecycle events.
type LifecycleState struct {
	// Instance Metadata (Static)
	InstanceID       string
	InstanceType     string
	AvailabilityZone string
	Region           string
	Lifecycle        string // "spot" or "on-demand"

	// Spot Termination
	TerminationImminent bool
	TerminationAction   string
	TerminationTime     time.Time

	// Rebalance Recommendation
	RebalanceRecommended bool
	RebalanceTime        time.Time

	// Scheduled Maintenance
	MaintenanceActive bool

	// Exporter Health
	IMDSAvailable       bool
	IMDSEventsAvailable bool
	IMDSVersion         int
	LastPollSuccessful  time.Time
}

// Store is a thread-safe cache for LifecycleState.
type Store struct {
	mu    sync.RWMutex
	state LifecycleState
}

// NewStore creates a new Store.
func NewStore() *Store {
	return &Store{}
}

// Get returns a copy of the current state.
func (s *Store) Get() LifecycleState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// Update updates the state using a provided function.
func (s *Store) Update(fn func(*LifecycleState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.state)
}
