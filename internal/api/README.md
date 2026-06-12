# API package

Candidate should implement the HTTP API layer here.

Recommended responsibilities:

- Route registration
- JSON request decoding
- JSON response encoding
- Input validation
- Structured error responses
- Calling the Store interface

Do not keep all API logic in `cmd/server/main.go`.
