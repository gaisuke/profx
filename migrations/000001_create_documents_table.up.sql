CREATE TYPE document_type AS ENUM ('cv', 'project_report');

CREATE TABLE documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type document_type NOT NULL,
    original_filename VARCHAR(255) NOT NULL,
    file_path VARCHAR(500) NOT NULL,
    uploaded_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_documents_type ON documents(type);
CREATE INDEX idx_documents_uploaded_at ON documents(uploaded_at);