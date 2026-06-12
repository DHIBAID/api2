package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"taskbridge/internal/executor"
	"taskbridge/internal/model"
	"time"
)

func main() {
	serverURL := flag.String("server", "http://localhost:8080", "TaskBridge server URL")
	agentID := flag.String("id", "agent-dev-1", "agent identifier")
	capabilities := flag.String("capabilities", "http_check,tcp_check,file_exists,checksum,copy_file,write_file,wait", "comma-separated job capabilities")
	pollInterval := flag.Duration("poll-interval", 3*time.Second, "job polling interval")
	flag.Parse()

	caps := parseCapabilities(*capabilities)
	client := &agentClient{
		baseURL:      strings.TrimRight(*serverURL, "/"),
		agentID:      *agentID,
		capabilities: caps,
		httpClient:   &http.Client{Timeout: 15 * time.Second},
		registry:     executor.NewDefaultRegistry(),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := client.register(ctx); err != nil {
		log.Fatal(err)
	}
	log.Printf("registered agent_id=%s server=%s capabilities=%v", *agentID, *serverURL, caps)

	go client.heartbeatLoop(ctx, 10*time.Second)
	client.pollLoop(ctx, *pollInterval)
}

type agentClient struct {
	baseURL      string
	agentID      string
	capabilities []model.JobType
	httpClient   *http.Client
	registry     *executor.Registry
}

func (c *agentClient) register(ctx context.Context) error {
	hostname, _ := os.Hostname()
	req := model.RegisterAgentRequest{
		AgentID:      c.agentID,
		Hostname:     hostname,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		Version:      "dev",
		Capabilities: c.capabilities,
	}
	return c.post(ctx, "/agents/register", req, nil)
}

func (c *agentClient) heartbeatLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.post(ctx, fmt.Sprintf("/agents/%s/heartbeat", c.agentID), map[string]any{}, nil); err != nil {
				log.Printf("heartbeat failed: %v", err)
			}
		}
	}
}

func (c *agentClient) pollLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := c.pollOnce(ctx); err != nil {
			log.Printf("poll failed: %v", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *agentClient) pollOnce(ctx context.Context) error {
	var next model.NextJobResponse
	err := c.post(ctx, fmt.Sprintf("/agents/%s/next-job", c.agentID), model.NextJobRequest{Capabilities: c.capabilities}, &next)
	if err != nil {
		return err
	}
	if next.Job == nil {
		return nil
	}

	job := *next.Job
	log.Printf("running job_id=%s name=%q type=%s attempt=%d", job.ID, job.Name, job.Type, job.AttemptCount)

	ex, ok := c.registry.Get(job.Type)
	if !ok {
		return c.report(ctx, job.ID, executor.Result{Status: model.JobFailed, Logs: []string{"unsupported job type"}, Error: "unsupported job type"})
	}

	runCtx := ctx
	cancel := func() {}
	if job.TimeoutSeconds > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(job.TimeoutSeconds)*time.Second)
	}
	defer cancel()

	result := ex.Execute(runCtx, job)
	if runCtx.Err() == context.DeadlineExceeded && result.Status != model.JobSuccess {
		result.Status = model.JobFailed
		result.Error = "job timed out"
		result.Logs = append(result.Logs, "job timed out")
	}
	if result.Status != model.JobSuccess {
		result.Status = model.JobFailed
	}

	if err := c.report(ctx, job.ID, result); err != nil {
		return err
	}
	log.Printf("reported job_id=%s status=%s", job.ID, result.Status)
	return nil
}

func (c *agentClient) report(ctx context.Context, jobID string, result executor.Result) error {
	req := model.JobResultRequest{
		Status: result.Status,
		Logs:   result.Logs,
		Result: result.Result,
		Error:  result.Error,
	}
	return c.post(ctx, fmt.Sprintf("/jobs/%s/result", jobID), req, nil)
}

func (c *agentClient) post(ctx context.Context, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr model.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		if apiErr.Error == "" {
			apiErr.Error = resp.Status
		}
		return fmt.Errorf("%s", apiErr.Error)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func parseCapabilities(raw string) []model.JobType {
	parts := strings.Split(raw, ",")
	caps := make([]model.JobType, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		caps = append(caps, model.JobType(part))
	}
	return caps
}
