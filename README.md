# profx

profx (pronounce Professor X) adalah layanan Go yang menilai CV. Ada tiga permukaan:

- **`/v1/checks`** — pencari kerja menempelkan satu lowongan dan satu CV, lalu dapat
  skor 0-100, tiga celah terbesar, dan satu contoh perbaikan. Publik, tanpa login.
- **`/v1/searches`** — pencari kerja memasukkan CV, layanan mengambil lowongan
  publik (Kalibrr untuk Indonesia, plus Remotive yang menerima pelamar dari
  Indonesia), menyaringnya tanpa biaya model, lalu menilai kandidat teratas satu
  per satu. Publik, tanpa login.
- **`/v1/documents` + `/v1/evaluations`** — sisi perekrut: unggah CV dan laporan
  proyek, jalankan evaluasi berlatarbelakang, ambil hasilnya.

Seluruh permukaan itu REST dengan satu konvensi. Kontrak tertulisnya ada di
[`docs/api-contract.md`](docs/api-contract.md), mesinnya di `internal/api`, dan
spesifikasi OpenAPI yang bisa dicoba ada di **`/v1/openapi.yaml`** dengan UI-nya di
`/profx/api/docs/`.

## Konvensi REST

- Jawaban sukses adalah representasi sumbernya langsung; tanpa pembungkus `ok`.
- Jawaban galat selalu `{"error":{"code","message","details"}}`. `message` berbahasa
  Indonesia dan boleh ditampilkan apa adanya; `details[].field` menyebut ruas mana
  yang salah, supaya frontend bisa menandai kotak isian yang tepat.
- Setiap jawaban membawa `X-Request-Id` (dikirim klien dipakai ulang bila aman,
  kalau tidak dibuat sendiri), dan endpoint berkuota membawa `X-RateLimit-Limit`,
  `-Remaining`, `-Reset`.
- CORS aktif untuk `/v1/*`; daftar origin diatur lewat `API_CORS_ORIGINS`
  (bawaan `*`).
- Jalur yang tidak ada dan metode yang salah pun dijawab dengan bentuk galat yang
  sama — bukan teks polos bawaan `net/http`, justru dua jawaban itulah yang paling
  sering ditemui saat integrasi.

## Menjalankan

```bash
go build -o bin/profx . && sudo install -m 0755 bin/profx /usr/local/bin/profx
sudo systemctl restart profx      # env di /etc/profx.env
```

Endpoint lokal: `http://127.0.0.1:8779/v1/health`. Publik:
`https://danimunf.duckdns.org/profx/api/v1/health`.

### Melihat apa yang "dilihat" pencari lowongan

```bash
go run ./cmd/probe -cv /tmp/cv.txt
```

Mengambil dari board sungguhan, mencetak jumlah per sumber dan negara teratas,
lalu daftar pendek beserta skor relevansi dan faktor keterjangkauannya — tanpa
panggilan model, tanpa database, tanpa memakai kuota.

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

## Public CV check (`/v1/checks`)

`/v1/checks` is the self-service side of profx: a job seeker pastes a job description
and their CV and gets a score, three concrete gaps, and one rewritten line. It
needs no account, no rubric corpus (the job description *is* the rubric), and no
Ragie — the retrieval path exists for the recruiter-side pipeline, and a visitor
has already handed us the only criteria that matter.

```
POST /v1/checks               multipart or JSON: job_desc, cv_text or cv_file (PDF), job_title
GET  /v1/checks/{id}          the stored result, while it is still alive
GET  /v1/checks/limits        quota left for this visitor
POST /v1/checks/{id}/interest interest in the paid part (waitlist)
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


## Job matching (`/v1/searches`)

The second self-service path: a CV goes in, ranked postings come out.

```
POST /v1/searches        multipart or JSON: cv_text or cv_file (PDF), location, remote_only, max_results
GET  /v1/searches/{id}   status, progress, and the ranked results
GET  /v1/searches/limits searches left for this visitor
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
GET /v1/health  # liveness, provider, sources, rubric-corpus metadata, and the
                # quotas. The corpus block replaces the old /retrieval-check:
                # a filter that matches nothing makes every evaluation run
                # without a rubric, and the only visible symptom is softer
                # feedback. It used to print
                      # many documents each configured filter actually matches
```

The corpus block in `/v1/health` exists because a filter mismatch is otherwise invisible:
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
- `GET /v1/health` reports the demo allowance and every quota the caller can spend
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
