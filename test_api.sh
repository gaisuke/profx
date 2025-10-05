#!/bin/bash

# Test script for profx API
# This script tests the /upload endpoint with sample PDF files

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# API endpoint
BASE_URL="${BASE_URL:-http://localhost:8080}"
UPLOAD_URL="$BASE_URL/upload"

echo "================================================"
echo "          profx API Test Script"
echo "================================================"
echo ""

# Create temporary test PDF files
echo "Creating test PDF files..."
mkdir -p /tmp/profx-test

# Create a minimal valid PDF for CV
cat > /tmp/profx-test/candidate_cv.pdf << 'EOF'
%PDF-1.4
1 0 obj
<</Type /Catalog /Pages 2 0 R>>
endobj
2 0 obj
<</Type /Pages /Kids [3 0 R] /Count 1>>
endobj
3 0 obj
<</Type /Page /Parent 2 0 R /Resources <</Font <</F1 <</Type /Font /Subtype /Type1 /BaseFont /Helvetica>>>>>> /MediaBox [0 0 612 792] /Contents 4 0 R>>
endobj
4 0 obj
<</Length 44>>
stream
BT
/F1 12 Tf
100 700 Td
(Candidate CV) Tj
ET
endstream
endobj
xref
0 5
0000000000 65535 f
0000000009 00000 n
0000000058 00000 n
0000000115 00000 n
0000000317 00000 n
trailer
<</Size 5 /Root 1 0 R>>
startxref
410
%%EOF
EOF

# Create a minimal valid PDF for project report
cat > /tmp/profx-test/project_report.pdf << 'EOF'
%PDF-1.4
1 0 obj
<</Type /Catalog /Pages 2 0 R>>
endobj
2 0 obj
<</Type /Pages /Kids [3 0 R] /Count 1>>
endobj
3 0 obj
<</Type /Page /Parent 2 0 R /Resources <</Font <</F1 <</Type /Font /Subtype /Type1 /BaseFont /Helvetica>>>>>> /MediaBox [0 0 612 792] /Contents 4 0 R>>
endobj
4 0 obj
<</Length 47>>
stream
BT
/F1 12 Tf
100 700 Td
(Project Report) Tj
ET
endstream
endobj
xref
0 5
0000000000 65535 f
0000000009 00000 n
0000000058 00000 n
0000000115 00000 n
0000000317 00000 n
trailer
<</Size 5 /Root 1 0 R>>
startxref
413
%%EOF
EOF

echo -e "${GREEN}✓ Test PDF files created${NC}"
echo ""

# Test 1: Successful upload
echo "Test 1: Uploading both files (should succeed)"
echo "----------------------------------------------"
echo "Command:"
echo "curl -X POST $UPLOAD_URL \\"
echo "  -F \"candidate_cv=@/tmp/profx-test/candidate_cv.pdf\" \\"
echo "  -F \"project_report=@/tmp/profx-test/project_report.pdf\""
echo ""

response=$(curl -s -w "\nHTTP_STATUS:%{http_code}" -X POST "$UPLOAD_URL" \
  -F "candidate_cv=@/tmp/profx-test/candidate_cv.pdf" \
  -F "project_report=@/tmp/profx-test/project_report.pdf")

http_body=$(echo "$response" | sed -e 's/HTTP_STATUS\:.*//g')
http_status=$(echo "$response" | tr -d '\n' | sed -e 's/.*HTTP_STATUS://')

echo "Response:"
echo "$http_body" | jq '.' 2>/dev/null || echo "$http_body"
echo ""

if [ "$http_status" -eq 201 ]; then
    echo -e "${GREEN}✓ Test 1 PASSED (HTTP $http_status)${NC}"
else
    echo -e "${RED}✗ Test 1 FAILED (HTTP $http_status)${NC}"
fi
echo ""
echo "================================================"
echo ""

# Test 2: Missing candidate_cv
echo "Test 2: Missing candidate_cv (should fail)"
echo "-------------------------------------------"
echo "Command:"
echo "curl -X POST $UPLOAD_URL \\"
echo "  -F \"project_report=@/tmp/profx-test/project_report.pdf\""
echo ""

response=$(curl -s -w "\nHTTP_STATUS:%{http_code}" -X POST "$UPLOAD_URL" \
  -F "project_report=@/tmp/profx-test/project_report.pdf")

http_body=$(echo "$response" | sed -e 's/HTTP_STATUS\:.*//g')
http_status=$(echo "$response" | tr -d '\n' | sed -e 's/.*HTTP_STATUS://')

echo "Response:"
echo "$http_body" | jq '.' 2>/dev/null || echo "$http_body"
echo ""

if [ "$http_status" -eq 400 ]; then
    echo -e "${GREEN}✓ Test 2 PASSED (HTTP $http_status)${NC}"
else
    echo -e "${RED}✗ Test 2 FAILED (HTTP $http_status)${NC}"
fi
echo ""
echo "================================================"
echo ""

# Test 3: Wrong HTTP method
echo "Test 3: Using GET instead of POST (should fail)"
echo "------------------------------------------------"
echo "Command:"
echo "curl -X GET $UPLOAD_URL"
echo ""

response=$(curl -s -w "\nHTTP_STATUS:%{http_code}" -X GET "$UPLOAD_URL")

http_body=$(echo "$response" | sed -e 's/HTTP_STATUS\:.*//g')
http_status=$(echo "$response" | tr -d '\n' | sed -e 's/.*HTTP_STATUS://')

echo "Response:"
echo "$http_body" | jq '.' 2>/dev/null || echo "$http_body"
echo ""

if [ "$http_status" -eq 405 ]; then
    echo -e "${GREEN}✓ Test 3 PASSED (HTTP $http_status)${NC}"
else
    echo -e "${RED}✗ Test 3 FAILED (HTTP $http_status)${NC}"
fi
echo ""
echo "================================================"
echo ""

# Test 4: Non-PDF file
echo "Test 4: Uploading non-PDF file (should fail)"
echo "---------------------------------------------"
echo "Creating test.txt file..."
echo "This is not a PDF" > /tmp/profx-test/test.txt

echo ""
echo "Command:"
echo "curl -X POST $UPLOAD_URL \\"
echo "  -F \"candidate_cv=@/tmp/profx-test/test.txt\" \\"
echo "  -F \"project_report=@/tmp/profx-test/project_report.pdf\""
echo ""

response=$(curl -s -w "\nHTTP_STATUS:%{http_code}" -X POST "$UPLOAD_URL" \
  -F "candidate_cv=@/tmp/profx-test/test.txt" \
  -F "project_report=@/tmp/profx-test/project_report.pdf")

http_body=$(echo "$response" | sed -e 's/HTTP_STATUS\:.*//g')
http_status=$(echo "$response" | tr -d '\n' | sed -e 's/.*HTTP_STATUS://')

echo "Response:"
echo "$http_body" | jq '.' 2>/dev/null || echo "$http_body"
echo ""

if [ "$http_status" -eq 400 ]; then
    echo -e "${GREEN}✓ Test 4 PASSED (HTTP $http_status)${NC}"
else
    echo -e "${RED}✗ Test 4 FAILED (HTTP $http_status)${NC}"
fi
echo ""
echo "================================================"
echo ""

# Cleanup
echo "Cleaning up test files..."
rm -rf /tmp/profx-test
echo -e "${GREEN}✓ Cleanup complete${NC}"
echo ""
echo "================================================"
echo "          Test Suite Complete"
echo "================================================"
