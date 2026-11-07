package builder

import (
	"fmt"
	"sync"
	"time"
)

type JobStatus string

const (
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
)

type JobType string

const (
	JobImage JobType = "image"
	JobQCOW2 JobType = "qcow2"
)

// Job tracks one build pipeline end to end.
type Job struct {
	ID        string    `json:"id"`
	Type      JobType   `json:"type"`
	Status    JobStatus `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	// filled for image jobs
	ImageRef string `json:"imageRef,omitempty"` // ref nodes pull: singularity:5000/os/edge-base:v1.0
	Digest   string `json:"digest,omitempty"`   // sha256:... of the pushed manifest

	// filled for qcow2 jobs
	ArtifactName string `json:"artifactName,omitempty"`
	ArtifactSum  string `json:"artifactSum,omitempty"`

	Error string `json:"error,omitempty"`
	Log   string `json:"log,omitempty"` // tail of build output
}

type Store struct {
	mu   sync.RWMutex
	jobs map[string]*Job
}

func NewStore() *Store {
	return &Store{jobs: make(map[string]*Job)}
}

func (s *Store) Create(id string, t JobType) *Job {
	j := &Job{ID: id, Type: t, Status: JobPending, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	s.mu.Lock()
	s.jobs[id] = j
	s.mu.Unlock()
	return j
}

func (s *Store) Get(id string) (*Job, error) {
	s.mu.RLock()
	j, ok := s.jobs[id]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("job %s not found", id)
	}
	return j, nil
}

// Snapshot returns a copy of the job so callers never read fields while the
// build goroutine is updating them.
func (s *Store) Snapshot(id string) (*Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, fmt.Errorf("job %s not found", id)
	}
	c := *j
	return &c, nil
}

// List returns copies of all jobs, for the ListJobs RPC.
func (s *Store) List() []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		c := *j
		out = append(out, &c)
	}
	return out
}

// Update mutates a job under the store lock and stamps UpdatedAt.
func (s *Store) Update(id string, fn func(*Job)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return
	}
	fn(j)
	j.UpdatedAt = time.Now()
}
