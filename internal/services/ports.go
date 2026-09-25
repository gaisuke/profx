package services

import "context"

// LLM is the generate-only view of a language model client. Interfaces are
// declared where they are consumed: this keeps the evaluation pipeline testable
// with a scripted fake and lets the provider be swapped (Gemini, OpenCode Go)
// without touching this package.
type LLM interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

// Retriever is the rubric-retrieval view of the RAG client.
type Retriever interface {
	RetrieveForCV(jobTitle string) (string, error)
	RetrieveForProject() (string, error)
}
