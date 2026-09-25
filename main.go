package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/go-playground/validator/v10"
	_ "github.com/lib/pq"

	"github.com/gaisuke/profx/internal/database"
	"github.com/gaisuke/profx/internal/handlers"
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
	ragieAPIKey := getEnv("RAGIE_API_KEY", "")
	var retriever services.Retriever
	if ragieAPIKey == "" {
		log.Printf("RAGIE_API_KEY is not set: rubric retrieval disabled, evaluations run without rubric context")
		retriever = ragie.NoopClient{}
	} else {
		retriever = ragie.NewClient(ragieAPIKey)
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
		)
		log.Printf("LLM provider: opencodego (model %s)", client.Model())
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
	uploadHandler := handlers.NewUploadHandler(documentService)
	evaluateHandler := handlers.NewEvaluateHandler(jobService, validate)
	resultHandler := handlers.NewResultHandler(jobService, validate)

	// Register routes
	http.Handle("/upload", uploadHandler)
	http.Handle("/evaluate", evaluateHandler)
	http.Handle("/result/", resultHandler)

	// Get port from environment or use default
	port := getEnv("SERVER_PORT", "8080")

	// Setup HTTP server
	srv := &http.Server{
		Addr: ":" + port,
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
