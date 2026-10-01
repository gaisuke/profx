package api

import _ "embed"

// The specification ships inside the binary. Serving it from the same process
// that implements it is the only way to keep /v1/openapi.yaml from describing a
// version of the API that is no longer running.
//
//go:embed openapi.yaml
var openAPISpec []byte

// OpenAPISpec exposes the embedded document for callers that want to write it to
// disk (a release artefact, or a client generator).
func OpenAPISpec() []byte { return openAPISpec }
