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
  "project_report_id": "fde18c07-d0f3-4db3-a607-84a257e5b233",
  "message": "Files uploaded successfully"
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
