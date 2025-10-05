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

```
profx/
├── main.go           # Main application file with HTTP server and upload handler
├── uploads/          # Directory where uploaded files are stored (gitignored)
├── go.mod            # Go module dependencies
├── go.sum            # Go module checksums
└── README.md         # This file
```

## Development

### Running Tests

```bash
go test ./...
```

### Building for Production

```bash
go build -ldflags="-s -w" -o profx-server main.go
```
