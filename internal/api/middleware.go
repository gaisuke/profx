package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// The CORS headers a browser needs before it will let another origin call this
// API at all. Without them a frontend on a different host gets an opaque
// failure, which is the single most common way an integration stalls.
const (
	headerRequestID  = "X-Request-Id"
	headerRateLimit  = "X-RateLimit-Limit"
	headerRemaining  = "X-RateLimit-Remaining"
	headerReset      = "X-RateLimit-Reset"
	headerAllowOrig  = "Access-Control-Allow-Origin"
	headerAllowMeth  = "Access-Control-Allow-Methods"
	headerAllowHdrs  = "Access-Control-Allow-Headers"
	headerMaxAge     = "Access-Control-Max-Age"
	headerExposeHdrs = "Access-Control-Expose-Headers"
)

// withMiddleware wraps the whole API: request id, panic recovery, CORS, and a
// body limit. Order matters — recovery outermost so a panic anywhere below still
// produces the documented error shape instead of a dropped connection.
func withMiddleware(next http.Handler, allowedOrigins []string, maxBody int64) http.Handler {
	// requestIDs wraps cors, not the other way round: a preflight is answered
	// inside cors and returns early, so with the old order that 204 was the one
	// response missing the identifier the contract promises on every response.
	return recoverer(requestIDs(cors(withLogging(limitBody(next, maxBody)), allowedOrigins)))
}

// requestIDs stamps an identifier on the exchange: echoed to the client, and put
// on the request so the logger and any error message can refer to the same one.
func requestIDs(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := requestID(r)
		r.Header.Set(headerRequestID, id)
		w.Header().Set(headerRequestID, id)
		next.ServeHTTP(w, r)
	})
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[api] panic di %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				writeError(w, http.StatusInternalServerError, codeInternal,
					"terjadi kesalahan di server. Coba lagi.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// cors answers preflight requests and tags real responses. A single "*" allows
// any origin, which is the right default for a public read-mostly API with no
// cookies; a deployment that wants to restrict callers sets API_CORS_ORIGINS.
func cors(next http.Handler, allowed []string) http.Handler {
	allowAll := false
	allowedSet := map[string]bool{}
	for _, o := range allowed {
		o = strings.TrimSpace(o)
		if o == "*" || o == "" {
			allowAll = true
			continue
		}
		allowedSet[strings.ToLower(o)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		switch {
		case allowAll:
			w.Header().Set(headerAllowOrig, "*")
		case origin != "" && allowedSet[strings.ToLower(origin)]:
			w.Header().Set(headerAllowOrig, origin)
			w.Header().Add("Vary", "Origin")
		}
		if w.Header().Get(headerAllowOrig) != "" {
			w.Header().Set(headerAllowMeth, "GET, POST, OPTIONS")
			w.Header().Set(headerAllowHdrs, "Content-Type, X-Request-Id")
			w.Header().Set(headerExposeHdrs, headerRequestID+", "+headerRateLimit+", "+headerRemaining+", "+headerReset)
			w.Header().Set(headerMaxAge, "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func limitBody(next http.Handler, maxBody int64) http.Handler {
	if maxBody <= 0 {
		maxBody = 8 << 20
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		}
		next.ServeHTTP(w, r)
	})
}

// requestID gives every exchange an identifier, so a report of "it failed" can
// be matched to a log line. A client-supplied id is kept if it is sane.
func requestID(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get(headerRequestID)); v != "" && len(v) <= 64 {
		safe := true
		for _, c := range v {
			if !(c == '-' || c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
				safe = false
				break
			}
		}
		if safe {
			return v
		}
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// statusRecorder remembers what was written so the logger can report it.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		// Health checks are polled constantly and say nothing when they work.
		if strings.HasSuffix(r.URL.Path, "/health") {
			return
		}
		log.Printf("[api] %s %s → %d (%d B, %s, req %s)",
			r.Method, r.URL.Path, rec.status, rec.bytes,
			time.Since(started).Round(time.Millisecond), r.Header.Get(headerRequestID))
	})
}

// jsonNotFound keeps the documented error shape even for the paths the router
// itself rejects. Go's ServeMux answers an unknown path or a wrong method with a
// plain-text body, and a client written against this API would choke on the one
// response it is most likely to hit while integrating.
type jsonNotFound struct {
	mux http.Handler
}

func (j jsonNotFound) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	buf := &bufferedResponse{header: http.Header{}}
	j.mux.ServeHTTP(buf, r)

	contentType := buf.header.Get("Content-Type")
	isPlain := contentType == "" || strings.HasPrefix(contentType, "text/plain")
	if isPlain && (buf.status == http.StatusNotFound || buf.status == http.StatusMethodNotAllowed) {
		for k, v := range buf.header {
			if k == "Content-Type" || k == "Content-Length" {
				continue
			}
			w.Header()[k] = v
		}
		if buf.status == http.StatusMethodNotAllowed {
			writeError(w, http.StatusMethodNotAllowed, codeMethodNotUsed,
				"metode "+r.Method+" tidak dipakai di "+r.URL.Path+". Lihat /profx/api/v1/openapi.yaml.")
			return
		}
		writeError(w, http.StatusNotFound, codeNotFound,
			"endpoint "+r.URL.Path+" tidak ada. Daftarnya ada di /profx/api/v1/openapi.yaml.")
		return
	}
	for k, v := range buf.header {
		w.Header()[k] = v
	}
	if buf.status != 0 {
		w.WriteHeader(buf.status)
	}
	_, _ = w.Write(buf.body.Bytes())
}

// bufferedResponse collects a response so it can be inspected before it is sent.
// Responses here are small JSON documents, so the buffer stays bounded in
// practice; the cap is a guard against a runaway handler.
type bufferedResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (b *bufferedResponse) Header() http.Header { return b.header }

func (b *bufferedResponse) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}

func (b *bufferedResponse) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	if b.body.Len() > 4<<20 {
		return 0, fmt.Errorf("jawaban terlalu besar untuk disangga")
	}
	return b.body.Write(p)
}
