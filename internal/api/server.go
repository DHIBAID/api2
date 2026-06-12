package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"taskbridge/internal/model"
	"taskbridge/internal/store"
)

type Server struct {
	store store.Store
	now   func() time.Time
}

func NewServer(store store.Store) *Server {
	return &Server{store: store, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /jobs", s.createJob)
	mux.HandleFunc("GET /jobs", s.listJobs)
	mux.HandleFunc("GET /jobs/{jobID}", s.getJob)
	mux.HandleFunc("POST /jobs/{jobID}/cancel", s.cancelJob)
	mux.HandleFunc("POST /jobs/{jobID}/result", s.submitResult)
	mux.HandleFunc("POST /agents/register", s.registerAgent)
	mux.HandleFunc("GET /agents", s.listAgents)
	mux.HandleFunc("POST /agents/{agentID}/heartbeat", s.heartbeat)
	mux.HandleFunc("POST /agents/{agentID}/next-job", s.nextJob)
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "taskbridge-server"})
}

func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	var req model.CreateJobRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := validateCreateJob(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	job := model.Job{
		ID:             newID("job"),
		Name:           req.Name,
		Type:           req.Type,
		Payload:        req.Payload,
		Status:         model.JobPending,
		CreatedAt:      s.now(),
		MaxRetries:     req.MaxRetries,
		TimeoutSeconds: req.TimeoutSeconds,
		Logs:           []string{"job created"},
	}
	created, err := s.store.CreateJob(job)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) listJobs(w http.ResponseWriter, _ *http.Request) {
	jobs, err := s.store.ListJobs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	job, ok, err := s.store.GetJob(r.PathValue("jobID"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	if err := s.store.CancelJob(r.PathValue("jobID")); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrJobNotFound) {
			status = http.StatusNotFound
		}
		if errors.Is(err, store.ErrInvalidState) {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": string(model.JobCanceled)})
}

func (s *Server) submitResult(w http.ResponseWriter, r *http.Request) {
	var req model.JobResultRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Status != model.JobSuccess && req.Status != model.JobFailed {
		writeError(w, http.StatusBadRequest, "status must be SUCCESS or FAILED")
		return
	}
	if err := s.store.CompleteJob(r.PathValue("jobID"), model.JobStatus(req.Status), req.Logs, req.Result, req.Error); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrJobNotFound) {
			status = http.StatusNotFound
		}
		if errors.Is(err, store.ErrInvalidState) {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	job, _, _ := s.store.GetJob(r.PathValue("jobID"))
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) registerAgent(w http.ResponseWriter, r *http.Request) {
	var req model.RegisterAgentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	if len(req.Capabilities) == 0 {
		writeError(w, http.StatusBadRequest, "capabilities are required")
		return
	}
	for _, capability := range req.Capabilities {
		if !supportedJobType(capability) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported capability %q", capability))
			return
		}
	}
	agent := model.Agent{
		ID:           req.AgentID,
		Hostname:     req.Hostname,
		OS:           req.OS,
		Arch:         req.Arch,
		Version:      req.Version,
		Capabilities: req.Capabilities,
		LastSeen:     s.now(),
		Status:       "online",
	}
	registered, err := s.store.RegisterAgent(agent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, registered)
}

func (s *Server) listAgents(w http.ResponseWriter, _ *http.Request) {
	agents, err := s.store.ListAgents()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agents)
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Heartbeat(r.PathValue("agentID")); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrAgentNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "online"})
}

func (s *Server) nextJob(w http.ResponseWriter, r *http.Request) {
	var req model.NextJobRequest
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	job, ok, err := s.store.AssignNextJob(r.PathValue("agentID"), req.Capabilities)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrAgentNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, model.NextJobResponse{})
		return
	}
	writeJSON(w, http.StatusOK, model.NextJobResponse{Job: &job})
}

func validateCreateJob(req model.CreateJobRequest) error {
	if strings.TrimSpace(req.Name) == "" {
		return errors.New("name is required")
	}
	if !supportedJobType(req.Type) {
		return fmt.Errorf("unsupported job type %q", req.Type)
	}
	if req.Payload == nil {
		return errors.New("payload is required")
	}
	if req.TimeoutSeconds < 0 {
		return errors.New("timeout_seconds must be >= 0")
	}
	if req.MaxRetries < 0 {
		return errors.New("max_retries must be >= 0")
	}
	return nil
}

func supportedJobType(t model.JobType) bool {
	switch t {
	case model.JobHTTPCheck, model.JobTCPCheck, model.JobFileExists, model.JobChecksum, model.JobCopyFile, model.JobWriteFile, model.JobWait:
		return true
	default:
		return false
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, model.ErrorResponse{Error: msg})
}

func newID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}
