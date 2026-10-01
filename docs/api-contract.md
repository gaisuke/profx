# profx REST API — kontrak v1 (beku)

Berkas ini sumber kebenaran. Kalau implementasi berbeda dari berkas ini, yang
salah adalah implementasinya.

## Dasar

- Semua endpoint ada di bawah **`/v1`** (backend). Dari luar, nginx memetakan
  `https://danimunf.duckdns.org/profx/api/` ke akar backend, jadi URL publiknya
  `/profx/api/v1/...`.
- Permintaan dan jawaban **JSON** (`application/json; charset=utf-8`), kecuali
  unggah berkas yang memakai `multipart/form-data`.
- **Jawaban sukses = representasi sumbernya langsung**, tanpa pembungkus `ok`.
  Koleksi memakai `{"data":[...],"meta":{...}}`.
- **Jawaban galat** selalu berbentuk:
  ```json
  {"error":{"code":"validation_failed","message":"kalimat bahasa Indonesia untuk pengguna",
            "details":[{"field":"cv_text","reason":"minimal 200 karakter"}]}}
  ```
  `details` opsional. `message` selalu bahasa Indonesia dan boleh ditampilkan apa
  adanya ke pengguna.
- Waktu: RFC3339 dengan zona (contoh `2026-10-01T10:16:22+07:00`).
- Setiap jawaban menyertakan `X-Request-Id` (dibuat kalau klien tidak mengirim).
- Endpoint berkuota menyertakan `X-RateLimit-Limit`, `X-RateLimit-Remaining`,
  `X-RateLimit-Reset` (RFC3339).
- **CORS** aktif untuk semua `/v1/*`: `API_CORS_ORIGINS` (daftar dipisah koma,
  bawaan `*`). Minta preflight `OPTIONS` dijawab `204` dengan header
  `Access-Control-Allow-Origin`, `-Methods` (`GET,POST,OPTIONS`),
  `-Headers` (`Content-Type, X-Request-Id`), `-Max-Age` (`600`).
- Batas isi: 8 MB untuk unggahan.

## Kode galat

Semua jawaban menyertakan `X-Request-Id`, termasuk preflight `OPTIONS`.

| `code` | HTTP | dipakai untuk |
|---|---|---|
| `validation_failed` | 400 | isi permintaan tidak memenuhi syarat |
| `not_found` | 404 | id tidak dikenal atau sudah kedaluwarsa |
| `quota_exceeded` | 429 | jatah gratis harian habis |
| `rate_limited` | 429 | terlalu banyak permintaan dari satu klien |
| `forbidden` | 403 | endpoint sengaja dimatikan pada deployment publik |
| `method_not_allowed` | 405 | metode HTTP salah untuk jalur itu |
| `unsupported_media_type` | 415 | bukan JSON/multipart |
| `payload_too_large` | 413 | isi melebihi batas |
| `upstream_failed` | 502 | model atau sumber data gagal |
| `internal_error` | 500 | sisanya |

## Alur rekruter (dokumen + evaluasi)

### POST /v1/documents
`multipart/form-data`: `candidate_cv` (PDF, wajib), `project_report` (PDF, wajib)
→ **201** `{"candidate_cv_id":"...","project_report_id":"..."}`

### POST /v1/evaluations
JSON `{"job_title":"Backend Engineer","cv_document_id":"...","report_document_id":"..."}`
← **202** `{"id":"...","status":"queued","job_title":"...","created_at":"..."}`

### GET /v1/evaluations/{id}
→ **200**
```json
{"id":"...","status":"completed","job_title":"...",
 "cv_match_rate":0.83,"cv_feedback":"...",
 "project_score":4.2,"project_feedback":"...","overall_summary":"...",
 "error_message":"","created_at":"...","completed_at":"..."}
```
`status`: `queued` | `processing` | `completed` | `failed`. Ruas angka bernilai
`null` selama belum selesai.

Nilai pertama adalah **`queued`**, bukan `pending`. Versi awal dokumen ini salah
menuliskannya, dan klien yang dibangun dari dokumen itu akan menolak status yang
sah. Sumber kebenarannya `internal/models/job.go` plus enum `job_status` di
Postgres — dokumen OpenAPI sudah benar sejak awal.

### GET /v1/evaluations?limit=20&offset=0
→ **200** `{"data":[evaluasi...],"meta":{"limit":20,"offset":0,"count":20}}`

## Cek CV (publik, tanpa login)

### POST /v1/checks
`multipart/form-data` atau JSON. Ruas: `job_title` (opsional), `job_desc` (wajib,
min 80 karakter), `cv_text` **atau** `cv_file` (PDF), `cf-turnstile-response`
(opsional).
→ **200**
```json
{"id":"fcjeadojrlqtoosh","job_title":"Backend Engineer (Go)","score":54,
 "result":{"score":54,"summary":"...","gaps":[{"section":"...","problem":"...","fix":"..."}],
           "sample_fix":{"before":"...","after":"...","reason":"..."}},
 "created_at":"...","expires_at":"...",
 "links":{"self":"/profx/api/v1/checks/fcjeadojrlqtoosh",
          "page":"/profx/cek/hasil.html?id=fcjeadojrlqtoosh"}}
```
`gaps` selalu 3. **CV dan job desc tidak disimpan**; hanya hasil ini, dan
`expires_at` biasanya 24 jam setelah dibuat.

### GET /v1/checks/{id}
→ **200** objek yang sama dengan di atas. Kedaluwarsa → **404**.

### GET /v1/checks/limits
→ **200**
```json
{"per_ip_per_day":3,"used":1,"remaining":2,"global_per_day":100,"global_used":1,
 "resets_at":"2026-10-02T00:00:00+07:00"}
```

### POST /v1/checks/{id}/interest
JSON `{"contact":"0812...","note":"opsional"}` → **201** `{"check_id":"...","status":"recorded"}`
Kontak kurang dari 5 karakter → **400** dengan `details[0].field = "contact"`.

## Pencarian lowongan

### POST /v1/searches
`multipart/form-data` atau JSON. Ruas: `cv_text` atau `cv_file` (PDF, min 200
karakter teks), `location` (opsional), `remote_only` (bool, opsional),
`max_results` (5–25, bawaan 10).
→ **202** `{"id":"...","status":"running","message":"mencari lowongan yang cocok…",
            "created_at":"...","expires_at":"..."}`

### GET /v1/searches/{id}
→ **200**
```json
{"id":"...","status":"running","message":"...","progress":{"scored":6,"total":10},
 "results":[{"score":52,"reason":"...","gaps":["..."],
             "job":{"title":"Backend Developer","company":"Trimegah Sekuritas Indonesia",
                    "location":"South Jakarta, Indonesia","remote":false,"source":"kalibrr",
                    "url":"https://www.kalibrr.com/c/...","published_at":"..."}}],
 "created_at":"...","expires_at":"..."}
```
`status`: `running` | `done` | `failed`. `results` terurut skor tertinggi dulu,
boleh kosong selama `running`. Kedaluwarsa → **404**.

### GET /v1/searches/limits
→ **200** bentuk sama dengan `checks/limits`.

## Operasional

### GET /v1/health
→ **200**
```json
{"status":"ok","provider":"opencodego",
 "retrieval":{"enabled":true,"base_url":"http://127.0.0.1:8780","filter_key":"kind",
              "cv_filter_values":["job_desc","cv_rubric"],
              "project_filter_values":["case_brief","project_rubric"]},
 "sources":["kalibrr","remotive"],
 "quotas":{"checks":{...},"searches":{...}}}
```
Blok `retrieval` menggantikan endpoint `/retrieval-check` yang lama: filter korpus
yang tidak cocok membuat evaluasi berjalan tanpa rubrik, dan satu-satunya gejala
adalah umpan balik yang terasa lebih lunak.

### GET /v1/openapi.yaml
→ **200** `application/yaml` — spesifikasi OpenAPI 3.0.3 yang menggambarkan
seluruh endpoint di atas.

## Dihapus (pindah ke v1, tanpa alias)

`/upload` → `/v1/documents`; `/evaluate` → `/v1/evaluations`;
`/result/{id}` → `/v1/evaluations/{id}`; `/results` → `/v1/evaluations`;
`/cek*` → `/v1/checks*`; `/cari*` → `/v1/searches*`; `/healthz`,
`/retrieval-check` → `/v1/health`.

Pemakai lama yang harus ikut diperbarui: `web/cek/index.html`,
`web/cari/index.html`, `test_api.sh`, dan nginx bila ada location khusus.
