package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"

	"github.com/gaisuke/profx/internal/api"
	"github.com/gaisuke/profx/internal/database"
	"github.com/gaisuke/profx/internal/jobs"
	"github.com/gaisuke/profx/internal/llm"
	"github.com/gaisuke/profx/internal/ragie"
	"github.com/gaisuke/profx/internal/services"
	"github.com/gaisuke/profx/internal/storage"
	"github.com/gaisuke/profx/internal/workers"
)

const uploadDir = "./uploads"

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	dbConfig := database.Config{
		Host:    getEnv("DB_HOST", "localhost"),
		Port:    getEnvAsInt("DB_PORT", 5432),
		User:    getEnv("DB_USER", ""),
		Pass:    getEnv("DB_PASSWORD", ""),
		DBName:  getEnv("DB_NAME", ""),
		SSLMode: getEnv("DB_SSLMODE", "disable"),
	}
	db, err := database.NewConnection(dbConfig)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer db.Close()

	jobQueue := make(chan string, getEnvAsInt("JOB_QUEUE_SIZE", 100))

	fileStorage, err := storage.NewFileStorage(uploadDir)
	if err != nil {
		log.Fatal("Failed to initialize file storage:", err)
	}
	documentRepo := storage.NewDocumentRepository(db)
	jobRepo := storage.NewJobRepository(db)

	// Retrieval is optional: without it the service still runs, and every
	// evaluation records that it scored without rubric context. It is enabled by
	// either a hosted API key or a self-hosted base URL — a corpus on this box
	// needs no key, and refusing to run without one would make that path unusable.
	ragieAPIKey := getEnv("RAGIE_API_KEY", "")
	ragieBase := getEnv("RAGIE_BASE_URL", "")
	var retriever services.Retriever
	var retrieverDiagnostics api.RetrieverDiagnostics
	if ragieAPIKey == "" && ragieBase == "" {
		log.Printf("no RAGIE_API_KEY or RAGIE_BASE_URL: rubric retrieval disabled, evaluations run without rubric context")
		noop := ragie.NoopClient{}
		retriever = noop
		retrieverDiagnostics = noop
	} else {
		client := ragie.NewClient(ragieAPIKey).WithBaseURL(ragieBase)
		retriever = client
		retrieverDiagnostics = client
		log.Printf("Retrieval enabled: %s (filter key %q)", client.BaseURL(), getEnv("RAGIE_FILTER_KEY", "type"))
	}

	provider := getEnv("LLM_PROVIDER", "opencodego")
	var llmClient services.LLM
	switch provider {
	case "opencodego":
		apiKey := getEnv("OPENCODE_GO_API_KEY", "")
		if apiKey == "" {
			log.Fatal("OPENCODE_GO_API_KEY is required when LLM_PROVIDER=opencodego")
		}
		client := llm.NewOpenCodeGoClient(
			apiKey,
			getEnv("OPENCODE_GO_BASE_URL", ""),
			getEnv("OPENCODE_GO_MODEL", ""),
			getEnvAsInt("OPENCODE_GO_MAX_TOKENS", 0),
		).WithTimeout(time.Duration(getEnvAsInt("OPENCODE_GO_TIMEOUT_SECONDS", 30)) * time.Second)
		log.Printf("LLM provider: opencodego (model %s, timeout %ss per attempt)",
			client.Model(), getEnv("OPENCODE_GO_TIMEOUT_SECONDS", "30"))
		llmClient = client
	case "gemini":
		geminiAPIKey := getEnv("GEMINI_API_KEY", "")
		if geminiAPIKey == "" {
			log.Fatal("GEMINI_API_KEY is required when LLM_PROVIDER=gemini")
		}
		client, err := llm.NewGeminiClient(context.Background(), geminiAPIKey, getEnv("GEMINI_MODEL", "gemini-1.5-flash"))
		if err != nil {
			log.Fatal("Failed to initialize Gemini client:", err)
		}
		log.Printf("LLM provider: gemini")
		llmClient = client
	default:
		log.Fatalf("unknown LLM_PROVIDER %q (expected gemini or opencodego)", provider)
	}

	documentService := services.NewDocumentService(fileStorage, documentRepo)
	evaluationService := services.NewEvaluationService(jobRepo, documentRepo, retriever, llmClient)
	jobService := services.NewJobService(jobRepo, documentRepo, jobQueue)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	numWorkers := getEnvAsInt("NUM_WORKERS", 5)
	workers.StartWorkerPool(ctx, &wg, numWorkers, jobQueue, evaluationService)

	// Public paths. Both are unauthenticated, so their cost is bounded three ways:
	// a per-visitor quota, an optional global daily cap, and the proxy's rate
	// limiting.
	cekRepo := storage.NewCekRepository(db)
	cekService := services.NewCekService(cekRepo, llmClient, services.CekConfig{
		PerIPLimit:  getEnvAsInt("CEK_PER_IP_PER_DAY", 3),
		GlobalLimit: getEnvAsInt("CEK_GLOBAL_PER_DAY", 100),
		TTL:         time.Duration(getEnvAsInt("CEK_TTL_HOURS", 24)) * time.Hour,
		IPSalt:      getEnv("CEK_IP_SALT", "profx-cek"),
		PaidOpen:    getEnv("CEK_PAID_OPEN", "false") == "true",
	})

	cariRepo := storage.NewCariRepository(db)
	cariSources := jobs.Select(strings.Split(getEnv("CARI_SOURCES", strings.Join(jobs.DefaultSourceNames(), ",")), ","))
	sourceNames := make([]string, 0, len(cariSources))
	for _, s := range cariSources {
		sourceNames = append(sourceNames, s.Name())
	}
	log.Printf("cari: sumber lowongan aktif: %s (fokus %s)",
		strings.Join(sourceNames, ", "), getEnv("CARI_HOME_COUNTRY", "Indonesia"))
	cariService := services.NewCariService(cariRepo, llmClient, cariSources, services.CariConfig{
		PerIPLimit:  getEnvAsInt("CARI_PER_IP_PER_DAY", 2),
		GlobalLimit: getEnvAsInt("CARI_GLOBAL_PER_DAY", 20),
		TTL:         time.Duration(getEnvAsInt("CARI_TTL_HOURS", 48)) * time.Hour,
		Keep:        getEnvAsInt("CARI_KEEP", 10),
		MaxAgeDays:  getEnvAsInt("CARI_MAX_AGE_DAYS", 75),
		Concurrency: getEnvAsInt("CARI_CONCURRENCY", 4),
		CacheMaxAge: time.Duration(getEnvAsInt("CARI_CACHE_HOURS", 336)) * time.Hour,
		IPSalt:      getEnv("CARI_IP_SALT", getEnv("CEK_IP_SALT", "profx-cek")),
		HomeCountry: getEnv("CARI_HOME_COUNTRY", "Indonesia"),
	})
	cariService.Start(ctx)

	// Sweepers. Reads already refuse expired rows; this deletes them, which is
	// what makes the retention promise on the pages verifiable.
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := cekService.Cleanup(ctx); err != nil {
					log.Printf("cek: pembersihan gagal: %v", err)
				}
				if _, err := cariService.Cleanup(ctx); err != nil {
					log.Printf("cari: pembersihan gagal: %v", err)
				}
			}
		}
	}()

	// Public demo: 0 disables the cap (private deployment). When set, the quota
	// bounds the model spend the recruiter endpoints can cause.
	demoMode := services.NewDemoMode(jobRepo, getEnvAsInt("PROFX_DEMO_MAX_EVALS_PER_DAY", 0))
	if demoMode.Enabled() {
		log.Printf("public demo mode: max %d evaluations per day (WIB)", demoMode.Limit())
	}

	var turnstile api.Turnstile
	if verifier := api.NewTurnstileVerifier(getEnv("TURNSTILE_SECRET", "")); verifier != nil {
		turnstile = verifier
	} else {
		log.Printf("cek: Turnstile tidak aktif (TURNSTILE_SECRET kosong) — andalan: kuota per pengunjung + rate limit di reverse proxy")
	}

	router := api.NewRouter(api.Deps{
		Documents: documentService,
		Jobs:      jobService,
		Checks:    cekService,
		Searches:  cariService,
		Demo:      demoMode,
		Retriever: retrieverDiagnostics,
		Turnstile: turnstile,
		Provider:  provider,
		Sources:   sourceNames,
		Origins:   strings.Split(getEnv("API_CORS_ORIGINS", "*"), ","),
		MaxBody:   int64(getEnvAsInt("API_MAX_BODY_MB", 8)) << 20,
		OpenAPI:   api.OpenAPISpec(),
	})

	port := getEnv("SERVER_PORT", "8080")
	addr := getEnv("SERVER_ADDR", "127.0.0.1")
	srv := &http.Server{Addr: net.JoinHostPort(addr, port), Handler: router}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("Server starting on %s", srv.Addr)
		log.Printf("REST surface (semua di bawah /v1, dokumentasi di /v1/openapi.yaml):")
		for _, line := range []string{
			"POST   /v1/documents",
			"POST   /v1/evaluations",
			"GET    /v1/evaluations",
			"GET    /v1/evaluations/{id}",
			"POST   /v1/checks",
			"GET    /v1/checks/limits",
			"GET    /v1/checks/{id}",
			"POST   /v1/checks/{id}/interest",
			"POST   /v1/searches",
			"GET    /v1/searches/limits",
			"GET    /v1/searches/{id}",
			"GET    /v1/health",
			"GET    /v1/openapi.yaml",
		} {
			log.Printf("  %s", line)
		}
		log.Printf("Worker pool: %d workers ready", numWorkers)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Server failed to start:", err)
		}
	}()

	<-sigChan
	log.Println("Received shutdown signal, gracefully shutting down...")
	cancel()
	close(jobQueue)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
	log.Println("Waiting for workers to finish current jobs...")
	wg.Wait()
	log.Println("All workers stopped. Shutdown complete.")
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(valueStr)
	if err != nil {
		log.Printf("Invalid integer for %s, using default: %d", key, defaultValue)
		return defaultValue
	}
	return value
}
