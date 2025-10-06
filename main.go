package main

import (
	"log"
	"net/http"
	"os"

	"github.com/gaisuke/profx/internal/handlers"
	"github.com/gaisuke/profx/internal/services"
	"github.com/gaisuke/profx/internal/storage"
)

const uploadDir = "./uploads"

func main() {
	// Initialize storage layer
	fileStorage, err := storage.NewFileStorage(uploadDir)
	if err != nil {
		log.Fatal("Failed to initialize storage:", err)
	}

	// Initialize service layer
	documentService := services.NewDocumentService(fileStorage)

	// Initialize handler layer
	uploadHandler := handlers.NewUploadHandler(documentService)

	// Register routes
	http.Handle("/upload", uploadHandler)
	
	// Get port from environment or use default
	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server starting on port %s...\n", port)
	log.Printf("Upload endpoint: POST http://localhost:%s/upload\n", port)
	
	// Start server
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal("Server failed to start:", err)
	}
}
