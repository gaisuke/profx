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

	"github.com/go-playground/validator/v10"
	_ "github.com/lib/pq"

	"github.com/gaisuke/profx/internal/database"
	"github.com/gaisuke/profx/internal/handlers"
	"github.com/gaisuke/profx/internal/jobs"
	"github.com/gaisuke/profx/internal/llm"
	"github.com/gaisuke/profx/internal/ragie"
	"github.com/gaisuke/profx/internal/services"
	"github.com/gaisuke/profx/internal/storage"
	"github.com/gaisuke/profx/internal/workers"
	"github.com/joho/godotenv"
)

const uploadDir = "./uploads"

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	// Database configuration
	dbConfig := database.Config{
		Host:    getEnv("DB_HOST", "localhost"),
		Port:    getEnvAsInt("DB_PORT", 5432),
		User:    getEnv("DB_USER", ""),
		Pass:    getEnv("DB_PASSWORD", ""),
		DBName:  getEnv("DB_NAME", ""),
		SSLMode: getEnv("DB_SSLMODE", "disable"),
	}

	// Connect to database
	db, err := database.NewConnection(dbConfig)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer db.Close()

	// Initialize job queue (buffered channel)
	jobQueueSize := getEnvAsInt("JOB_QUEUE_SIZE", 100)
	jobQueue := make(chan string, jobQueueSize)

	// Initialize repositories
	fileStorage, err := storage.NewFileStorage(uploadDir)
	if err != nil {
		log.Fatal("Failed to initialize file storage:", err)
	}

	documentRepo := storage.NewDocumentRepository(db)
	jobRepo := storage.NewJobRepository(db)

	// Initialize AI clients
	// Retrieval is optional: without a key the service still runs, and every
	// evaluation records that it ran without rubric context.
	// Retrieval is enabled by either a hosted API key or a self-hosted base URL:
	// a corpus on this box needs no key, and refusing to run without one would
	// make the self-hosted path unusable.
	ragieAPIKey := getEnv("RAGIE_API_KEY", "")
	ragieBase := getEnv("RAGIE_BASE_URL", "")
	var retriever services.Retriever
	var retrieverDiagnostics handlers.RetrieverDiagnostics
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

	// Initialize the LLM client. LLM_PROVIDER picks the provider; both implement
	// the same interface, so the evaluation pipeline is provider-agnostic.
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
		log.Printf("LLM provider: opencodego (model %s, timeout %s per attempt)",
			client.Model(), getEnv("OPENCODE_GO_TIMEOUT_SECONDS", "30")+"s")
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

	// Initialize services
	documentService := services.NewDocumentService(fileStorage, documentRepo)
	evaluationService := services.NewEvaluationService(jobRepo, documentRepo, retriever, llmClient)
	jobService := services.NewJobService(jobRepo, documentRepo, jobQueue)

	// Start worker pool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	numWorkers := getEnvAsInt("NUM_WORKERS", 5)
	workers.StartWorkerPool(ctx, &wg, numWorkers, jobQueue, evaluationService)

	// Initialize request validator
	validate := validator.New()

	// Initialize handlers
	// Public demo: 0 disables the cap (private deployment). When set, the quota
	// bounds the model spend an open endpoint can cause, and the API reports it.
	demoMode := services.NewDemoMode(jobRepo, getEnvAsInt("PROFX_DEMO_MAX_EVALS_PER_DAY", 0))
	if demoMode.Enabled() {
		log.Printf("public demo mode: max %d evaluations per day (WIB)", demoMode.Limit())
	}

	uploadHandler := handlers.NewUploadHandler(documentService)
	evaluateHandler := handlers.NewEvaluateHandler(jobService, validate, demoMode)
	resultHandler := handlers.NewResultHandler(jobService, validate)
	resultsHandler := handlers.NewResultsHandler(jobService, demoMode)
	opsHandler := handlers.NewOpsHandler(retrieverDiagnostics, provider, demoMode)

	// Public CV check (/cek): the self-service side. No authentication, so its
	// cost is bounded by a per-visitor quota, an optional global daily cap, and
	// the reverse proxy's rate limiting.
	cekRepo := storage.NewCekRepository(db)
	cekService := services.NewCekService(cekRepo, llmClient, services.CekConfig{
		PerIPLimit:  getEnvAsInt("CEK_PER_IP_PER_DAY", 3),
		GlobalLimit: getEnvAsInt("CEK_GLOBAL_PER_DAY", 100),
		TTL:         time.Duration(getEnvAsInt("CEK_TTL_HOURS", 24)) * time.Hour,
		IPSalt:      getEnv("CEK_IP_SALT", "profx-cek"),
		PaidOpen:    getEnv("CEK_PAID_OPEN", "false") == "true",
	})
	turnstile := handlers.NewTurnstileVerifier(getEnv("TURNSTILE_SECRET", ""))
	if turnstile == nil {
		log.Printf("cek: Turnstile tidak aktif (TURNSTILE_SECRET kosong) — andalan: kuota per pengunjung + rate limit di reverse proxy")
	}
	cekHandler := handlers.NewCekHandler(cekService, turnstile)

	// Job matching (/cari): the CV goes in, ranked postings come out. Postings
	// come from public boards; the model only ever sees the dozen that a free
	// keyword filter says are worth judging.
	cariRepo := storage.NewCariRepository(db)
	cariSources := jobs.Select(strings.Split(getEnv("CARI_SOURCES", strings.Join(jobs.DefaultSourceNames(), ",")), ","))
	sourceNames := make([]string, 0, len(cariSources))
	for _, s := range cariSources {
		sourceNames = append(sourceNames, s.Name())
	}
	log.Printf("cari: sumber lowongan aktif: %s (fokus %s)", strings.Join(sourceNames, ", "), getEnv("CARI_HOME_COUNTRY", "Indonesia"))
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
	cariHandler := handlers.NewCariHandler(cariService, turnstile)

	// Register routes
	http.Handle("/upload", uploadHandler)
	http.Handle("/evaluate", evaluateHandler)
	http.Handle("/result/", resultHandler)
	http.Handle("/results", resultsHandler)
	http.Handle("/cek", cekHandler)
	http.Handle("/cek/", cekHandler)
	http.Handle("/cari", cariHandler)
	http.Handle("/cari/", cariHandler)
	http.Handle("/healthz", opsHandler)
	http.Handle("/retrieval-check", opsHandler)

	// Retention is a promise on the page, so it gets a sweeper rather than a
	// hope. Reads already refuse expired rows; this deletes them.
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

	// Get port from environment or use default. The bind address defaults to
	// loopback: this API takes CVs and has no authentication of its own, so it
	// must only be reachable through the reverse proxy that wraps it in basic
	// auth. SERVER_ADDR exists for the case where something else fronts it.
	port := getEnv("SERVER_PORT", "8080")
	addr := getEnv("SERVER_ADDR", "127.0.0.1")

	// Setup HTTP server
	srv := &http.Server{
		Addr: net.JoinHostPort(addr, port),
	}

	// Handle graceful
	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start server in a goroutine
	go func() {
		log.Printf("Server starting on port %s...\n", port)
		log.Printf("Endpoints:\n")
		log.Printf("  POST   http://localhost:%s/upload\n", port)
		log.Printf("  POST   http://localhost:%s/evaluate\n", port)
		log.Printf("  GET    http://localhost:%s/result/{id}\n", port)
		log.Printf("  GET    http://localhost:%s/results?limit=N\n", port)
		log.Printf("  POST   http://localhost:%s/cek            (publik, tanpa login)\n", port)
		log.Printf("  GET    http://localhost:%s/cek/hasil/{id} (publik)\n", port)
		log.Printf("  GET    http://localhost:%s/cek/info       (publik)\n", port)
		log.Printf("  POST   http://localhost:%s/cek/minat      (publik)\n", port)
		log.Printf("  GET    http://localhost:%s/healthz\n", port)
		log.Printf("  GET    http://localhost:%s/retrieval-check\n", port)
		log.Printf("Worker pool: %d workers ready\n", numWorkers)

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Server failed to start:", err)
		}
	}()

	// Wait for interrupt signal
	<-sigChan
	log.Println("\nReceived shutdown signal, gracefully shutting down...")

	// Cancel context to stop workers
	cancel()

	// Close job queue (no more jobs accepted)
	close(jobQueue)

	// Shutdown HTTP server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	// Wait for all workers to finish
	log.Println("Waiting for workers to finish current jobs...")
	wg.Wait()

	log.Println("All workers stopped. Shutdown complete.")
}

// Helper functions for environment variables
func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
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
