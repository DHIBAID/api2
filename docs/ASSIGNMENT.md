# TaskBridge Assignment

Build a cross-platform remote job runner in Go.

The starter project intentionally provides only:

- Go module setup
- server and agent entry points
- domain models
- Store interface
- Executor interface
- basic `/health` endpoint
- examples and README

The candidate must implement the actual server-agent system.

## Required implementation

1. HTTP APIs for jobs and agents
2. In-memory concurrency-safe store
3. Agent registration and heartbeat
4. Agent polling for jobs
5. Job lifecycle handling
6. Job result submission
7. Retry and timeout logic
8. Safe job executors
9. Validation and structured errors
10. Tests and documentation

## Stretch goals

- SQLite persistence
- Auth token
- Job cancellation
- Web dashboard
- Metrics endpoint
- Dockerfile
- GitHub Actions
