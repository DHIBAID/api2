package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"taskbridge/internal/model"
)

func TestWriteFileAndChecksumExecutors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	writeResult := WriteFileExecutor{}.Execute(context.Background(), model.Job{
		Type:    model.JobWriteFile,
		Payload: map[string]any{"path": path, "content": "hello"},
	})
	if writeResult.Status != model.JobSuccess {
		t.Fatalf("write_file failed: %+v", writeResult)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello" {
		t.Fatalf("unexpected content %q", string(body))
	}

	checksumResult := ChecksumExecutor{}.Execute(context.Background(), model.Job{
		Type:    model.JobChecksum,
		Payload: map[string]any{"path": path},
	})
	if checksumResult.Status != model.JobSuccess {
		t.Fatalf("checksum failed: %+v", checksumResult)
	}
	expected := sha256.Sum256([]byte("hello"))
	if checksumResult.Result["checksum"] != hex.EncodeToString(expected[:]) {
		t.Fatalf("unexpected checksum: %+v", checksumResult.Result)
	}
}

func TestWaitExecutorHonorsContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	result := WaitExecutor{}.Execute(ctx, model.Job{
		Type:    model.JobWait,
		Payload: map[string]any{"duration_seconds": 1},
	})
	if result.Status != model.JobFailed {
		t.Fatalf("expected failed timeout result, got %+v", result)
	}
}
