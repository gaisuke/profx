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

	// Initialize storage layers
	fileStorage, err := storage.NewFileStorage(uploadDir)
	if err != nil {
		log.Fatal("Failed to initialize file storage:", err)
	}

	documentRepo := storage.NewDocumentRepository(db)

	// Initialize service layer
	documentService := services.NewDocumentService(fileStorage, documentRepo)

	// Initialize handler layer
	uploadHandler := handlers.NewUploadHandler(documentService)

	// Register routes
	http.Handle("/upload", uploadHandler)

	// Get port from environment or use default
	port := getEnv("SERVER_PORT")

	log.Printf("Server starting on port %s...\n", port)
	log.Printf("Upload endpoint: POST http://localhost:%s/upload\n", port)

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
