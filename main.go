package main

import (
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gaisuke/profx/internal/database"
	"github.com/gaisuke/profx/internal/handlers"
	"github.com/gaisuke/profx/internal/services"
	"github.com/gaisuke/profx/internal/storage"
	"github.com/go-playground/validator/v10"
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
		Host:    getEnv("DB_HOST"),
		Port:    getEnvAsInt("DB_PORT"),
		User:    getEnv("DB_USER"),
		Pass:    getEnv("DB_PASSWORD"),
		DBName:  getEnv("DB_NAME"),
		SSLMode: getEnv("DB_SSLMODE"),
	}

	// Connect to database
	db, err := database.NewConnection(dbConfig)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer db.Close()

	validator := validator.New()

	jobQueueSize := getEnvAsInt("JOB_QUEUE_SIZE")
	if jobQueueSize <= 0 {
		jobQueueSize = 100 // default queue size
	}
	jobQueue := make(chan string, jobQueueSize)

	// Initialize storage layers
	fileStorage, err := storage.NewFileStorage(uploadDir)
	if err != nil {
		log.Fatal("Failed to initialize file storage:", err)
	}

	documentRepo := storage.NewDocumentRepository(db)
	jobRepo := storage.NewJobRepository(db)

	// Initialize service layer
	documentService := services.NewDocumentService(fileStorage, documentRepo)
	jobService := services.NewJobService(jobRepo, documentRepo, jobQueue)

	// Initialize handler layer
	uploadHandler := handlers.NewUploadHandler(documentService)
	evaluateHandler := handlers.NewEvaluateHandler(jobService, validator)
	resultHandler := handlers.NewResultHandler(jobService, validator)

	// Register routes
	http.Handle("/upload", uploadHandler)
	http.Handle("/evaluate", evaluateHandler)
	http.Handle("/result/", resultHandler)

	// Get port from environment or use default
	port := getEnv("SERVER_PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server starting on port %s...\n", port)
	log.Printf("Upload endpoint: POST http://localhost:%s/upload\n", port)
	log.Printf("Evaluate endpoint: POST http://localhost:%s/evaluate\n", port)
	log.Printf("Result endpoint: GET http://localhost:%s/result/{job_id}\n", port)

	// Start server
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal("Server failed to start:", err)
	}
}

// Helper functions for environment variables
func getEnv(key string) string {
	value := os.Getenv(key)
	return value
}

func getEnvAsInt(key string) int {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return 0
	}
	value, err := strconv.Atoi(valueStr)
	if err != nil {
		log.Printf("Invalid integer for %s, using default: %d", key, 0)
		return 0
	}
	return value
}
