package store

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"taskbridge/internal/model"
)

const AgentOfflineAfter = 30 * time.Second

var (
	ErrJobNotFound   = errors.New("job not found")
	ErrAgentNotFound = errors.New("agent not found")
	ErrInvalidState  = errors.New("invalid job state")
)

// Store defines the required persistence operations.
// Candidate should first implement an in-memory store, then optionally add SQLite.
type Store interface {
	CreateJob(job model.Job) (model.Job, error)
	ListJobs() ([]model.Job, error)
	GetJob(jobID string) (model.Job, bool, error)
	CancelJob(jobID string) error

	RegisterAgent(agent model.Agent) (model.Agent, error)
	Heartbeat(agentID string) error
	ListAgents() ([]model.Agent, error)

	AssignNextJob(agentID string, capabilities []model.JobType) (model.Job, bool, error)
	CompleteJob(jobID string, status model.JobStatus, logs []string, result map[string]any, errMsg string) error
}

type MemoryStore struct {
	mu     sync.Mutex
	jobs   map[string]model.Job
	agents map[string]model.Agent
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		jobs:   map[string]model.Job{},
		agents: map[string]model.Agent{},
	}
}

func (s *MemoryStore) CreateJob(job model.Job) (model.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if job.ID == "" {
		return model.Job{}, fmt.Errorf("job id is required")
	}
	if _, exists := s.jobs[job.ID]; exists {
		return model.Job{}, fmt.Errorf("job %q already exists", job.ID)
	}
	s.jobs[job.ID] = cloneJob(job)
	return cloneJob(job), nil
}

func (s *MemoryStore) ListJobs() ([]model.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobs := make([]model.Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, cloneJob(job))
	}
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.Before(jobs[j].CreatedAt)
	})
	return jobs, nil
}

func (s *MemoryStore) GetJob(jobID string) (model.Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.jobs[jobID]
	if !ok {
		return model.Job{}, false, nil
	}
	return cloneJob(job), true, nil
}

func (s *MemoryStore) CancelJob(jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.jobs[jobID]
	if !ok {
		return ErrJobNotFound
	}
	if job.Status == model.JobSuccess || job.Status == model.JobFailed || job.Status == model.JobCanceled {
		return ErrInvalidState
	}
	now := time.Now().UTC()
	job.Status = model.JobCanceled
	job.FinishedAt = &now
	job.Logs = append(job.Logs, "job canceled")
	s.jobs[jobID] = job
	return nil
}

func (s *MemoryStore) RegisterAgent(agent model.Agent) (model.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if agent.ID == "" {
		return model.Agent{}, fmt.Errorf("agent id is required")
	}
	if agent.LastSeen.IsZero() {
		agent.LastSeen = time.Now().UTC()
	}
	agent.Status = "online"
	s.agents[agent.ID] = cloneAgent(agent)
	return cloneAgent(agent), nil
}

func (s *MemoryStore) Heartbeat(agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, ok := s.agents[agentID]
	if !ok {
		return ErrAgentNotFound
	}
	agent.LastSeen = time.Now().UTC()
	agent.Status = "online"
	s.agents[agentID] = agent
	return nil
}

func (s *MemoryStore) ListAgents() ([]model.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	agents := make([]model.Agent, 0, len(s.agents))
	for _, agent := range s.agents {
		agent.Status = agentStatus(agent.LastSeen, now)
		agents = append(agents, cloneAgent(agent))
	}
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].ID < agents[j].ID
	})
	return agents, nil
}

func (s *MemoryStore) AssignNextJob(agentID string, capabilities []model.JobType) (model.Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, ok := s.agents[agentID]
	if !ok {
		return model.Job{}, false, ErrAgentNotFound
	}
	agent.LastSeen = time.Now().UTC()
	agent.Status = "online"
	if len(capabilities) > 0 {
		agent.Capabilities = append([]model.JobType(nil), capabilities...)
	}
	s.agents[agentID] = agent

	var chosen *model.Job
	for _, job := range s.jobs {
		if job.Status != model.JobPending && job.Status != model.JobRetrying {
			continue
		}
		if !hasCapability(capabilitiesOrAgent(capabilities, agent.Capabilities), job.Type) {
			continue
		}
		jobCopy := job
		if chosen == nil || jobCopy.CreatedAt.Before(chosen.CreatedAt) {
			chosen = &jobCopy
		}
	}
	if chosen == nil {
		return model.Job{}, false, nil
	}

	now := time.Now().UTC()
	chosen.Status = model.JobRunning
	chosen.AssignedAgentID = agentID
	chosen.StartedAt = &now
	chosen.FinishedAt = nil
	chosen.AttemptCount++
	chosen.Error = ""
	chosen.Logs = append(chosen.Logs, fmt.Sprintf("attempt %d assigned to %s", chosen.AttemptCount, agentID))
	s.jobs[chosen.ID] = *chosen
	return cloneJob(*chosen), true, nil
}

func (s *MemoryStore) CompleteJob(jobID string, status model.JobStatus, logs []string, result map[string]any, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.jobs[jobID]
	if !ok {
		return ErrJobNotFound
	}
	if job.Status != model.JobRunning {
		return ErrInvalidState
	}

	job.Logs = append(job.Logs, logs...)
	job.Result = cloneMap(result)
	job.Error = errMsg
	now := time.Now().UTC()

	switch status {
	case model.JobSuccess:
		job.Status = model.JobSuccess
		job.FinishedAt = &now
	case model.JobFailed:
		if job.AttemptCount <= job.MaxRetries {
			job.Status = model.JobRetrying
			job.AssignedAgentID = ""
			job.StartedAt = nil
			job.FinishedAt = nil
			job.Logs = append(job.Logs, fmt.Sprintf("retry scheduled after attempt %d of %d", job.AttemptCount, job.MaxRetries+1))
		} else {
			job.Status = model.JobFailed
			job.FinishedAt = &now
		}
	default:
		return fmt.Errorf("unsupported completion status %q", status)
	}

	s.jobs[jobID] = job
	return nil
}

func hasCapability(capabilities []model.JobType, jobType model.JobType) bool {
	return slices.Contains(capabilities, jobType)
}

func capabilitiesOrAgent(requested []model.JobType, registered []model.JobType) []model.JobType {
	if len(requested) > 0 {
		return requested
	}
	return registered
}

func agentStatus(lastSeen time.Time, now time.Time) string {
	if now.Sub(lastSeen) > AgentOfflineAfter {
		return "offline"
	}
	return "online"
}

func cloneJob(job model.Job) model.Job {
	job.Payload = cloneMap(job.Payload)
	job.Logs = append([]string(nil), job.Logs...)
	job.Result = cloneMap(job.Result)
	return job
}

func cloneAgent(agent model.Agent) model.Agent {
	agent.Capabilities = append([]model.JobType(nil), agent.Capabilities...)
	return agent
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
