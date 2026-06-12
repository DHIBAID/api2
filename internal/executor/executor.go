package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"taskbridge/internal/model"
)

// Result is returned after executing a job.
type Result struct {
	Status model.JobStatus `json:"status"`
	Logs   []string        `json:"logs"`
	Result map[string]any  `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Executor executes a single job type.
type Executor interface {
	Type() model.JobType
	Execute(ctx context.Context, job model.Job) Result
}

// Registry maps job types to executors.
type Registry struct {
	executors map[model.JobType]Executor
}

func NewRegistry() *Registry {
	return &Registry{executors: map[model.JobType]Executor{}}
}

func (r *Registry) Register(ex Executor) {
	r.executors[ex.Type()] = ex
}

func (r *Registry) Get(t model.JobType) (Executor, bool) {
	ex, ok := r.executors[t]
	return ex, ok
}

func NewDefaultRegistry() *Registry {
	registry := NewRegistry()
	registry.Register(HTTPCheckExecutor{})
	registry.Register(TCPCheckExecutor{})
	registry.Register(FileExistsExecutor{})
	registry.Register(ChecksumExecutor{})
	registry.Register(CopyFileExecutor{})
	registry.Register(WriteFileExecutor{})
	registry.Register(WaitExecutor{})
	return registry
}

type HTTPCheckExecutor struct{}

func (HTTPCheckExecutor) Type() model.JobType { return model.JobHTTPCheck }

func (HTTPCheckExecutor) Execute(ctx context.Context, job model.Job) Result {
	url, ok := stringPayload(job.Payload, "url")
	if !ok || url == "" {
		return failed("missing payload.url")
	}
	expected, ok := intPayload(job.Payload, "expected_status")
	if !ok {
		expected = http.StatusOK
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return failed(err.Error())
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return failed(err.Error())
	}
	defer resp.Body.Close()

	result := map[string]any{"url": url, "status_code": resp.StatusCode, "expected_status": expected}
	if resp.StatusCode != expected {
		return Result{
			Status: model.JobFailed,
			Logs:   []string{fmt.Sprintf("GET %s returned %d, expected %d", url, resp.StatusCode, expected)},
			Result: result,
			Error:  "unexpected status code",
		}
	}
	return Result{Status: model.JobSuccess, Logs: []string{fmt.Sprintf("GET %s returned %d", url, resp.StatusCode)}, Result: result}
}

type TCPCheckExecutor struct{}

func (TCPCheckExecutor) Type() model.JobType { return model.JobTCPCheck }

func (TCPCheckExecutor) Execute(ctx context.Context, job model.Job) Result {
	address, ok := stringPayload(job.Payload, "address")
	if !ok || address == "" {
		host, hostOK := stringPayload(job.Payload, "host")
		port, portOK := intPayload(job.Payload, "port")
		if !hostOK || !portOK {
			return failed("missing payload.address or payload.host/payload.port")
		}
		address = net.JoinHostPort(host, fmt.Sprint(port))
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return failed(err.Error())
	}
	_ = conn.Close()
	return Result{Status: model.JobSuccess, Logs: []string{fmt.Sprintf("tcp connection to %s succeeded", address)}, Result: map[string]any{"address": address}}
}

type FileExistsExecutor struct{}

func (FileExistsExecutor) Type() model.JobType { return model.JobFileExists }

func (FileExistsExecutor) Execute(_ context.Context, job model.Job) Result {
	path, ok := stringPayload(job.Payload, "path")
	if !ok || path == "" {
		return failed("missing payload.path")
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{Status: model.JobFailed, Logs: []string{fmt.Sprintf("%s does not exist", path)}, Error: "file does not exist", Result: map[string]any{"path": path, "exists": false}}
		}
		return failed(err.Error())
	}
	return Result{Status: model.JobSuccess, Logs: []string{fmt.Sprintf("%s exists", path)}, Result: map[string]any{"path": path, "exists": true, "size": info.Size()}}
}

type ChecksumExecutor struct{}

func (ChecksumExecutor) Type() model.JobType { return model.JobChecksum }

func (ChecksumExecutor) Execute(ctx context.Context, job model.Job) Result {
	path, ok := stringPayload(job.Payload, "path")
	if !ok || path == "" {
		return failed("missing payload.path")
	}
	file, err := os.Open(path)
	if err != nil {
		return failed(err.Error())
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := copyWithContext(ctx, hash, file); err != nil {
		return failed(err.Error())
	}
	sum := hex.EncodeToString(hash.Sum(nil))
	return Result{Status: model.JobSuccess, Logs: []string{fmt.Sprintf("sha256 calculated for %s", path)}, Result: map[string]any{"path": path, "algorithm": "sha256", "checksum": sum}}
}

type CopyFileExecutor struct{}

func (CopyFileExecutor) Type() model.JobType { return model.JobCopyFile }

func (CopyFileExecutor) Execute(ctx context.Context, job model.Job) Result {
	src, srcOK := stringPayload(job.Payload, "source")
	dst, dstOK := stringPayload(job.Payload, "destination")
	if !srcOK || !dstOK || src == "" || dst == "" {
		return failed("missing payload.source or payload.destination")
	}
	in, err := os.Open(src)
	if err != nil {
		return failed(err.Error())
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return failed(err.Error())
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return failed(err.Error())
	}
	defer out.Close()

	written, err := copyWithContext(ctx, out, in)
	if err != nil {
		return failed(err.Error())
	}
	return Result{Status: model.JobSuccess, Logs: []string{fmt.Sprintf("copied %d bytes from %s to %s", written, src, dst)}, Result: map[string]any{"source": src, "destination": dst, "bytes": written}}
}

type WriteFileExecutor struct{}

func (WriteFileExecutor) Type() model.JobType { return model.JobWriteFile }

func (WriteFileExecutor) Execute(_ context.Context, job model.Job) Result {
	path, pathOK := stringPayload(job.Payload, "path")
	content, contentOK := stringPayload(job.Payload, "content")
	if !pathOK || !contentOK || path == "" {
		return failed("missing payload.path or payload.content")
	}
	appendMode, _ := boolPayload(job.Payload, "append")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return failed(err.Error())
	}
	if appendMode {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return failed(err.Error())
		}
		if _, err := file.WriteString(content); err != nil {
			_ = file.Close()
			return failed(err.Error())
		}
		_ = file.Close()
	} else if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return failed(err.Error())
	}
	return Result{Status: model.JobSuccess, Logs: []string{fmt.Sprintf("wrote %d bytes to %s", len(content), path)}, Result: map[string]any{"path": path, "bytes": len(content)}}
}

type WaitExecutor struct{}

func (WaitExecutor) Type() model.JobType { return model.JobWait }

func (WaitExecutor) Execute(ctx context.Context, job model.Job) Result {
	seconds, ok := intPayload(job.Payload, "duration_seconds")
	if !ok {
		return failed("missing payload.duration_seconds")
	}
	if seconds < 0 {
		return failed("duration_seconds must be >= 0")
	}
	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return failed(ctx.Err().Error())
	case <-timer.C:
		return Result{Status: model.JobSuccess, Logs: []string{fmt.Sprintf("waited %d seconds", seconds)}, Result: map[string]any{"duration_seconds": seconds}}
	}
}

func failed(msg string) Result {
	return Result{Status: model.JobFailed, Logs: []string{msg}, Error: msg}
}

func stringPayload(payload map[string]any, key string) (string, bool) {
	v, ok := payload[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func intPayload(payload map[string]any, key string) (int, bool) {
	v, ok := payload[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func boolPayload(payload map[string]any, key string) (bool, bool) {
	v, ok := payload[key]
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[:nr])
			written += int64(nw)
			if ew != nil {
				return written, ew
			}
			if nr != nw {
				return written, io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				return written, nil
			}
			return written, er
		}
	}
}
