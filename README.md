# TaskBridge

TaskBridge is a small Go remote job runner. A server accepts jobs, tracks agents, assigns compatible pending jobs, and records job results. Agents register with the server, send heartbeats, poll for work, run safe executors, and report logs/results.

## Implemented Features

- `POST /jobs`, `GET /jobs`, `GET /jobs/{jobId}`
- `POST /agents/register`, `POST /agents/{agentId}/heartbeat`, `GET /agents`
- `POST /agents/{agentId}/next-job`
- `POST /jobs/{jobId}/result`
- `GET /health`
- Optional `POST /jobs/{jobId}/cancel`
- Mutex-safe in-memory job and agent store
- Job lifecycle: `PENDING -> RUNNING -> SUCCESS`, `PENDING -> RUNNING -> FAILED`, and retry flow through `RETRYING`
- `timeout_seconds`, `max_retries`, and `attempt_count`
- Agent online/offline status based on heartbeat freshness
- Safe executors: `http_check`, `tcp_check`, `file_exists`, `checksum`, `copy_file`, `write_file`, `wait`
- Unit tests for API flow, retry lifecycle, heartbeat status, and executors

## Requirements

- Go 1.22+

## Run

Start the server:

```bash
go run ./cmd/server --addr :8080
```

Start an agent in another terminal:

```bash
go run ./cmd/agent --server http://localhost:8080 --id agent-dev-1
```

The default agent capabilities are:

```text
http_check,tcp_check,file_exists,checksum,copy_file,write_file,wait
```

## Demo

Create a job:

```bash
curl -sS -X POST http://localhost:8080/jobs \
  -H 'Content-Type: application/json' \
  --data @examples/create-http-check-job.json
```

List jobs and watch the status move from `PENDING` to `RUNNING` to `SUCCESS`:

```bash
curl -sS http://localhost:8080/jobs
```

List agents:

```bash
curl -sS http://localhost:8080/agents
```

Health check:

```bash
curl -sS http://localhost:8080/health
```

## API Examples

Create a wait job:

```bash
curl -sS -X POST http://localhost:8080/jobs \
  -H 'Content-Type: application/json' \
  --data @examples/create-wait-job.json
```

Create a file write job:

```bash
curl -sS -X POST http://localhost:8080/jobs \
  -H 'Content-Type: application/json' \
  --data @examples/create-write-file-job.json
```

Create a TCP check job:

```bash
curl -sS -X POST http://localhost:8080/jobs \
  -H 'Content-Type: application/json' \
  --data @examples/create-tcp-check-job.json
```

Fetch one job:

```bash
curl -sS http://localhost:8080/jobs/{job_id}
```

Cancel a pending, retrying, or running job:

```bash
curl -sS -X POST http://localhost:8080/jobs/{job_id}/cancel
```

## Job Payloads

`http_check`:

```json
{
  "url": "http://localhost:8080/health",
  "expected_status": 200
}
```

`tcp_check`:

```json
{
  "address": "localhost:8080"
}
```

`file_exists`:

```json
{
  "path": "/tmp/taskbridge-output.txt"
}
```

`checksum`:

```json
{
  "path": "/tmp/taskbridge-output.txt"
}
```

`copy_file`:

```json
{
  "source": "/tmp/taskbridge-output.txt",
  "destination": "/tmp/taskbridge-output-copy.txt"
}
```

`write_file`:

```json
{
  "path": "/tmp/taskbridge-output.txt",
  "content": "hello from taskbridge\n",
  "append": false
}
```

`wait`:

```json
{
  "duration_seconds": 5
}
```

## Tests

Run the full suite:

```bash
go test ./...
```

In restricted environments where the default Go cache is read-only:

```bash
GOCACHE=/tmp/taskbridge-go-cache go test ./...
```

## Notes

State is in memory, so jobs and agents are reset when the server restarts. The optional `command` executor is intentionally not implemented because it should be disabled or allowlisted by default.
