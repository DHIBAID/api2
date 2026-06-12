package store

import (
	"testing"
	"time"

	"taskbridge/internal/model"
)

func TestMemoryStoreRetryLifecycle(t *testing.T) {
	s := NewMemoryStore()
	_, err := s.RegisterAgent(model.Agent{
		ID:           "agent-1",
		Capabilities: []model.JobType{model.JobWait},
		LastSeen:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateJob(model.Job{
		ID:         "job-1",
		Name:       "retry-me",
		Type:       model.JobWait,
		Payload:    map[string]any{"duration_seconds": 0},
		Status:     model.JobPending,
		CreatedAt:  time.Now().UTC(),
		MaxRetries: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	assigned, ok, err := s.AssignNextJob("agent-1", nil)
	if err != nil || !ok {
		t.Fatalf("assign failed ok=%v err=%v", ok, err)
	}
	if assigned.ID != created.ID || assigned.Status != model.JobRunning || assigned.AttemptCount != 1 {
		t.Fatalf("unexpected first assignment: %+v", assigned)
	}

	err = s.CompleteJob(created.ID, model.JobFailed, []string{"first failed"}, nil, "boom")
	if err != nil {
		t.Fatal(err)
	}
	job, _, _ := s.GetJob(created.ID)
	if job.Status != model.JobRetrying {
		t.Fatalf("expected RETRYING, got %s", job.Status)
	}

	assigned, ok, err = s.AssignNextJob("agent-1", nil)
	if err != nil || !ok {
		t.Fatalf("retry assign failed ok=%v err=%v", ok, err)
	}
	if assigned.AttemptCount != 2 {
		t.Fatalf("expected second attempt, got %d", assigned.AttemptCount)
	}
	err = s.CompleteJob(created.ID, model.JobSuccess, []string{"ok"}, map[string]any{"done": true}, "")
	if err != nil {
		t.Fatal(err)
	}
	job, _, _ = s.GetJob(created.ID)
	if job.Status != model.JobSuccess || job.FinishedAt == nil {
		t.Fatalf("expected success with finished_at, got %+v", job)
	}
}

func TestListAgentsMarksOffline(t *testing.T) {
	s := NewMemoryStore()
	_, err := s.RegisterAgent(model.Agent{
		ID:           "agent-old",
		Capabilities: []model.JobType{model.JobWait},
		LastSeen:     time.Now().UTC().Add(-AgentOfflineAfter - time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	agents, err := s.ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	if got := agents[0].Status; got != "offline" {
		t.Fatalf("expected offline, got %s", got)
	}
}
