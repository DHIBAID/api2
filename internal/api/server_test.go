package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"taskbridge/internal/model"
	"taskbridge/internal/store"
)

func TestJobAgentAssignmentFlow(t *testing.T) {
	srv := NewServer(store.NewMemoryStore()).Routes()

	agent := postJSON[model.Agent](t, srv, "/agents/register", model.RegisterAgentRequest{
		AgentID:      "agent-1",
		Capabilities: []model.JobType{model.JobWait},
	})
	if agent.ID != "agent-1" {
		t.Fatalf("unexpected agent: %+v", agent)
	}

	job := postJSON[model.Job](t, srv, "/jobs", model.CreateJobRequest{
		Name:           "wait",
		Type:           model.JobWait,
		Payload:        map[string]any{"duration_seconds": 0},
		TimeoutSeconds: 1,
		MaxRetries:     0,
	})
	if job.Status != model.JobPending || job.ID == "" {
		t.Fatalf("unexpected created job: %+v", job)
	}

	next := postJSON[model.NextJobResponse](t, srv, "/agents/agent-1/next-job", model.NextJobRequest{
		Capabilities: []model.JobType{model.JobWait},
	})
	if next.Job == nil || next.Job.ID != job.ID || next.Job.Status != model.JobRunning {
		t.Fatalf("unexpected next job: %+v", next)
	}

	completed := postJSON[model.Job](t, srv, "/jobs/"+job.ID+"/result", model.JobResultRequest{
		Status: model.JobSuccess,
		Logs:   []string{"done"},
		Result: map[string]any{"ok": true},
	})
	if completed.Status != model.JobSuccess {
		t.Fatalf("expected success, got %+v", completed)
	}
}

func TestCreateJobValidation(t *testing.T) {
	srv := NewServer(store.NewMemoryStore()).Routes()
	body, _ := json.Marshal(model.CreateJobRequest{Name: "bad", Type: "command", Payload: map[string]any{}})
	req := httptest.NewRequest(http.MethodPost, "/jobs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func postJSON[T any](t *testing.T, handler http.Handler, path string, payload any) T {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("POST %s failed: status=%d body=%s", path, rec.Code, rec.Body.String())
	}
	var out T
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	return out
}
