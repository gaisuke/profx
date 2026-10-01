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

## Public CV check (/cek)

`/cek` is the self-service side of profx: a job seeker pastes a job description
and their CV and gets a score, three concrete gaps, and one rewritten line. It
needs no account, no rubric corpus (the job description *is* the rubric), and no
Ragie — the retrieval path exists for the recruiter-side pipeline, and a visitor
has already handed us the only criteria that matter.

```
POST /cek              multipart: job_desc, cv_text or cv_file (PDF), job_title
GET  /cek/hasil/{id}   the stored result, while it is still alive
GET  /cek/info         quota left for this visitor
POST /cek/minat        interest in the paid part (waitlist)
```

What is stored, and what is not:

- Stored: the score, the findings, a short job label, a salted SHA-256 of the
  client IP, and the timestamps. Expired rows are refused on read and deleted by
  an hourly sweeper; `CEK_TTL_HOURS` (default 24) is the retention promise.
- Not stored: the CV text and the job description. An uploaded PDF is parsed in
  memory and discarded — it never reaches `uploads/`. The tests assert this by
  building the stored row and checking the CV body and job description body are
  absent from it.

Cost control, because this is the one unauthenticated endpoint that spends model
money: `CEK_PER_IP_PER_DAY` (default 3) per visitor, `CEK_GLOBAL_PER_DAY`
(default 100) across the deployment, the reverse proxy's `limit_req`, and an
optional Cloudflare Turnstile check (`TURNSTILE_SECRET` — empty means off, and
the service says so at startup). Only successful checks consume quota, so a
model timeout does not cost a visitor a turn. `OPENCODE_GO_TIMEOUT_SECONDS`
(default 30) bounds one attempt: a visitor-facing check wants a longer leash
than the async recruiter pipeline.

The result id is 80 bits of randomness in base32, because the id is the only
thing protecting a result page.


## Job matching (/cari)

The second self-service path: a CV goes in, ranked postings come out.

```
POST /cari             multipart: cv_text or cv_file (PDF), lokasi, hanya_remote, jumlah
GET  /cari/hasil/{id}  status, progress, and the ranked results
GET  /cari/info        searches left for this visitor
```

**Two stages, because the model is the expensive part.** 579 postings arrive in
about a second from four boards; a free keyword relevance pass (title matches
weigh 6x a body mention, tags 3x) cuts them to the dozen worth judging. Only
those get a model call, one posting per call — a batch prompt would be cheaper
per posting but the scores drift as soon as the batch composition changes, and
consistency is the one property this product can honestly claim.

**The market is Indonesia** (`CARI_HOME_COUNTRY`), and that decides both the
sources and the ranking. `kalibrr` is the primary board — 318 Indonesian postings
in under two seconds, the only one here that states a city — queried with
`country=Indonesia` and the CV's own keywords, two pages per keyword, because its
default listing is South-East Asia-wide and comes back Philippines-heavy.
`remotive` stays for remote roles, filtered by its own
`candidate_required_location` so that "Worldwide" passes and "USA" or "Northern
America, LATAM, Europe" does not.

`remoteok` and `arbeitnow` are implemented, tested and switchable
(`CARI_SOURCES=kalibrr,remotive,remoteok,arbeitnow`) but off by default: remoteok
states no eligibility (its location field is empty or a US city, and an empty
field is not evidence that a candidate in Jakarta can take the job) and arbeitnow
is dominated by on-site German roles. Serving those is how the first live run
recommended Berlin on-site work to a Jakarta candidate.

**Reachability gates the ranking, not just the order.** With a preferred country
set: Indonesian postings are kept, remote postings are kept when their own text
admits Indonesia (discounted when it says nothing at all), and remote postings
restricted elsewhere or on-site abroad are dropped before the model ever sees
them. Without a preferred country the gate only orders what it is given.

**Spend the model on the right role.** Keyword relevance alone let a backend CV
fill four of six short-list slots with "Data Engineer" roles: "engineer" and
"postgresql" matched and the deciding word did not. Titles are now discounted by
how much of their own wording the CV has any claim to (0.6 at worst, so a title
using an unused synonym is ranked below a plain match rather than buried).

### Seeing what the matcher sees (`cmd/probe`)

```bash
go run ./cmd/probe -cv /tmp/cv.txt                  # Indonesian focus by default
go run ./cmd/probe -cv /tmp/cv.txt -sources kalibrr,remotive,remoteok -keep 15
```

It fetches from the real boards, prints per-source counts and the top countries,
then shows the short list with each candidate's relevance score and reachability
factor. No model calls, no database, no quota — the cheapest way to answer "is a
board quiet, is the eligibility rule too strict, is the ranking preferring the
wrong kind of posting".

**Cost control.** `CARI_PER_IP_PER_DAY` (2) and `CARI_GLOBAL_PER_DAY` (20) bound
how many searches run; `CARI_KEEP` (10) bounds model calls per search;
`CARI_CONCURRENCY` (4) bounds how many run at once. `job_scores` caches a
judgement per (CV fingerprint, posting) for `CARI_CACHE_HOURS` (336), so
searching again with the same CV costs nothing for postings already judged.

**Privacy, same rule as /cek.** The CV text lives in the queue entry in memory
and is never written down; only a SHA-256 fingerprint is stored, which is what
makes the score cache work without keeping the CV. A restart drops in-flight
searches rather than resuming them, and that is the intended trade. Postings
themselves are stored (they are public), results expire after `CARI_TTL_HOURS`
(48).

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

### The public demo

`PROFX_DEMO_MAX_EVALS_PER_DAY=0` (default) means a private deployment with no cap.
Setting it turns on public-demo behaviour:

- `POST /evaluate` is refused with `429` once the day's budget is spent, and the
  refusal names the reset time. The check runs before any job is created, and a
  broken counter fails closed rather than allowing unlimited spend.
- `GET /results` (history) returns `403`: a public demo must not list other
  visitors' evaluations. Individual results stay reachable by their UUID, which
  is the id the visitor already holds.
- `GET /healthz` reports `demo: {enabled, used, limit, remaining, resets_at}` and
  the UI shows the remaining count plus a "do not upload real personal data"
  notice. A quota nobody can see is indistinguishable from a bug.

Rate limiting belongs to the reverse proxy; the daily cap belongs here, because
only the application knows how much work it accepted.

Old uploads are deleted by `deploy/prune-uploads.sh` (run from cron): the demo
accepts files from strangers, so they should not accumulate forever.

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
