# profx
profx (pronounce Professor X) is an AI evaluator for job screening process. User input candidate's CV and returns a summarize of whether user match the job criteria or not.

## Features

- RESTful API endpoint for uploading documents
- Multipart form-data support for PDF files
- Unique ID generation for each uploaded file
- File validation and error handling

## Getting Started

### Prerequisites

- Go 1.21 or higher

### Installation

1. Clone the repository:
```bash
git clone https://github.com/gaisuke/profx.git
cd profx
```

2. Install dependencies:
```bash
go mod download
```

3. Build the application:
```bash
go build -o profx-server main.go
```

### Running the Server

Start the server:
```bash
./profx-server
```

The server will start on port 8080 by default. You can customize the port by setting the `PORT` environment variable:
```bash
PORT=3000 ./profx-server
```

## API Documentation

### POST /upload

Upload candidate CV and project report documents.

**Content-Type:** `multipart/form-data`

**Request Parameters:**
- `candidate_cv` (required): PDF file containing the candidate's CV
- `project_report` (required): PDF file containing the project report

**Success Response (201 Created):**
```json
{
  "candidate_cv_id": "d6caccc4-35dc-4e2b-b1c3-4548bcc9e532",
  "project_report_id": "fde18c07-d0f3-4db3-a607-84a257e5b233"
}
```

**Error Response (4xx/5xx):**
```json
{
  "error": "Error message describing what went wrong"
}
```

**Example using curl:**
```bash
curl -X POST http://localhost:8080/upload \
  -F "candidate_cv=@/path/to/cv.pdf" \
  -F "project_report=@/path/to/report.pdf"
```

**Error Cases:**
- `400 Bad Request`: Missing required files or non-PDF files
- `405 Method Not Allowed`: Using HTTP method other than POST
- `500 Internal Server Error`: Server-side error during file storage

## Project Structure

The project follows a clean architecture pattern with clear separation of concerns:

```
profx/
├── main.go                           # Application entry point
├── internal/                         # Internal application code
│   ├── handlers/                     # HTTP handlers (presentation layer)
│   │   └── upload_handler.go        # Upload endpoint handler
│   ├── services/                     # Business logic layer
│   │   └── document_service.go      # Document processing service
│   ├── storage/                      # Storage layer (repository pattern)
│   │   ├── storage.go                # Storage interface
│   │   └── file_storage.go          # File system implementation
│   └── models/                       # Data models
│       └── document.go               # Document-related models
├── uploads/                          # Directory for uploaded files (gitignored)
├── test_api.sh                       # API test script
├── go.mod                            # Go module dependencies
├── go.sum                            # Go module checksums
└── README.md                         # This file
```

### Architecture

The application follows these design patterns:

- **Layered Architecture**: Clear separation between handlers, services, and storage
- **Dependency Injection**: Dependencies are injected through constructors
- **Interface-based Design**: Storage layer uses interfaces for easy mocking and testing
- **Repository Pattern**: Storage abstraction allows switching implementations (file system, S3, etc.)

This structure makes the codebase:
- Easy to test (each layer can be tested independently)
- Maintainable (clear separation of concerns)
- Scalable (easy to add new endpoints and features)
- Flexible (easy to swap implementations)

## Testing the API

### Quick Test with cURL

Test the upload endpoint with sample files:

```bash
# Make sure the server is running first
./profx-server &

# Upload both files
curl -X POST http://localhost:8080/upload \
  -F "candidate_cv=@/path/to/your/cv.pdf" \
  -F "project_report=@/path/to/your/report.pdf"
```

### Automated Test Suite

Run the included test script to validate all API functionality:

```bash
# Make sure the server is running first
./profx-server &

# Run the test suite
./test_api.sh
```

The test suite validates:
- ✓ Successful file upload with valid PDFs
- ✓ Error handling for missing files
- ✓ HTTP method validation
- ✓ File type validation (PDF only)

## Development

### Running Tests

```bash
go test ./...
```

### Building for Production

```bash
go build -ldflags="-s -w" -o profx-server main.go
```

### Adding New Endpoints

Thanks to the layered architecture, adding new endpoints is straightforward:

1. **Add Model** (if needed): Define your request/response structures in `internal/models/`
2. **Add Storage** (if needed): Implement storage operations in `internal/storage/`
3. **Add Service**: Implement business logic in `internal/services/`
4. **Add Handler**: Create HTTP handler in `internal/handlers/`
5. **Register Route**: Register the handler in `main.go`

Example of registering a new handler:
```go
// In main.go
newHandler := handlers.NewYourHandler(yourService)
http.Handle("/your-endpoint", newHandler)
```

## Testing and evaluation

```bash
go test ./...                                    # unit tests, no network
EVAL_ENABLE=1 OPENCODE_GO_API_KEY=... \
  go test -tags eval ./internal/services/ -run TestEvalHarness -v   # scoring quality
```

Unit tests cover the scoring maths (a project score of 4.2 must never come back as
0.042), JSON repair of fenced or truncated model replies, retry classification
(a 429 retries, a 400 does not) and the provider client against a local HTTP
server. The evaluation harness measures agreement with labelled expectations,
drift across repeats and structured-output failure rate — see `eval/README.md`.

## LLM providers

`LLM_PROVIDER` selects the provider; both speak through the same interface, so the
pipeline is provider-agnostic:

- `opencodego` (default) — Anthropic Messages format at
  `https://opencode.ai/zen/go/v1/messages`. The gateway sits behind Cloudflare
  (a browser `User-Agent` is required) and demands a session header.
- `gemini` — Google Gen AI SDK.

## Deployment

Runs as a systemd unit with its own database and its own env file:

```bash
sudo -u postgres psql -c "CREATE ROLE profx LOGIN"
sudo -u postgres psql -c "CREATE DATABASE profx OWNER profx"
psql "host=127.0.0.1 user=profx dbname=profx" \
  -f migrations/000001_create_documents_table.up.sql \
  -f migrations/000002_create_evaluation_jobs_table.up.sql \
  -f migrations/000003_widen_project_score_range.up.sql   # in order
go build -o profx . && sudo install -m 755 profx /usr/local/bin/profx
sudo install -m 600 -o root -g root deploy/profx.env /etc/profx.env   # secrets, root-only
sudo install -m 644 deploy/profx.service /etc/systemd/system/profx.service
sudo systemctl daemon-reload && sudo systemctl enable --now profx
```

The unit runs with `WorkingDirectory=/var/lib/profx` (uploads land in
`/var/lib/profx/uploads`), `ProtectSystem=strict` and `ReadWritePaths=/var/lib/profx`.
The server listens on `SERVER_PORT` (8779 on this host) and is bound to localhost;
exposing it publicly needs a reverse proxy plus authentication.

`RAGIE_API_KEY` is optional: without it the service still starts and every
evaluation is produced without rubric context. The prompt then tells the model not
to invent criteria and to prefix its feedback with `RUBRIC UNAVAILABLE:`, so a
reviewer can see the score was made without the rubric.

### Health and diagnostics

```
GET /healthz          # liveness + which integrations are wired (provider, ragie)
GET /retrieval-check  # one live retrieval, plus the corpus metadata and how
                      # many documents each configured filter actually matches
```

`/retrieval-check` exists because a filter mismatch is otherwise invisible:
retrieval answers 200 with an empty result, the pipeline scores without a rubric,
and the only symptom is softer feedback. The check prints the corpus's real
metadata values next to the filter being sent, so the mismatch is one request
away from being found.

### Web UI

`web/index.html` is a single static page (no build step): upload a CV and a
report, watch the job, read the scores, browse history, and see retrieval status.
It calls the API under a relative `api/` prefix, so it is served next to a proxy
that maps `/profx/api/` to the service. Scores produced without the rubric are
labelled as such in the UI, not hidden.

### Where rubrics come from

`RAGIE_BASE_URL` selects the retrieval service. On this host it points at
`rubrikd` (127.0.0.1:8780), a self-hosted corpus of the rubric documents, and
`RAGIE_FILTER_KEY=kind` matching the corpus tags (`cv_rubric`, `job_desc`,
`case_brief`, `project_rubric`). Without a base URL or an API key, retrieval is
disabled and evaluations run without rubric context — visibly, never silently.

### Retrieval contract (Ragie)

The retrieval client speaks the documented API, and the three details that broke
it are worth remembering:

- the endpoint is `POST /retrievals` (plural); `/retrieve` does not exist
- the request field is `filter` (singular) and takes operators, e.g.
  `{"type": {"$in": ["job_desc", "cv_rubric"]}}` — not `"job_desc, cv_rubric"`
- the response field is `scored_chunks`, not `chunks`; decoding the wrong key
  yielded an empty slice with no error, so retrieval looked successful while
  handing the model no criteria at all

A retrieval that matches no chunk is reported as an error (`ragie: retrieval
matched no chunks`, naming the filter), never as an empty success.

### Score scales (do not "fix" these again)

- `cv_match_rate` is 0.00-1.00.
- `project_score` is on the rubric's own scale, **1.00-5.00**, and the database
  CHECK constraint was widened in migration 000003 to match. An earlier shared
  normaliser divided any score above 1 by 100 so a 4.2 could satisfy a 0-1
  constraint; that stored 0.042 and silently corrupted results.
