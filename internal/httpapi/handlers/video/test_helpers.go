package video

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	chi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/mock"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	importuc "athena/internal/usecase/import"
)

// Response is an alias for shared.Response for tests
type Response = shared.Response

// ErrorInfo is an alias for shared.ErrorInfo for tests
type ErrorInfo = shared.ErrorInfo

// Meta is an alias for shared.Meta for tests
type Meta = shared.Meta

// MockImportService is a mock implementation of the import service
type MockImportService struct {
	mock.Mock
}

// MockURLValidator is a mock implementation of URLValidator for testing
type MockURLValidator struct {
	mock.Mock
}

func (m *MockURLValidator) ValidateVideoURL(urlStr string) error {
	args := m.Called(urlStr)
	return args.Error(0)
}

func (m *MockImportService) ImportVideo(ctx context.Context, req *importuc.ImportRequest) (*domain.VideoImport, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.VideoImport), args.Error(1)
}

func (m *MockImportService) CancelImport(ctx context.Context, importID, userID string) error {
	args := m.Called(ctx, importID, userID)
	return args.Error(0)
}

func (m *MockImportService) RetryImport(ctx context.Context, importID, userID string) error {
	args := m.Called(ctx, importID, userID)
	return args.Error(0)
}

func (m *MockImportService) GetImport(ctx context.Context, importID, userID string) (*domain.VideoImport, error) {
	args := m.Called(ctx, importID, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.VideoImport), args.Error(1)
}

func (m *MockImportService) ListUserImports(ctx context.Context, userID string, limit, offset int) ([]*domain.VideoImport, int, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, 0, args.Error(2)
	}
	return args.Get(0).([]*domain.VideoImport), args.Int(1), args.Error(2)
}

func (m *MockImportService) ProcessPendingImports(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockImportService) CleanupOldImports(ctx context.Context, daysOld int) (int64, error) {
	args := m.Called(ctx, daysOld)
	return args.Get(0).(int64), args.Error(1)
}

// ipfsAddResponse represents a single line in IPFS add NDJSON output
//
//nolint:unused // used in test files
type ipfsAddResponse struct {
	Name string `json:"Name"`
	Hash string `json:"Hash"`
	Size string `json:"Size"`
}

// parseIPFSAddResponse parses the final CID from an ipfs add NDJSON stream.
//
//nolint:unused // used in test files
func parseIPFSAddResponse(r io.Reader) (string, error) {
	var last ipfsAddResponse
	// Use a scanner to read line-delimited JSON objects
	sc := bufio.NewScanner(r)
	// Increase buffer for large JSON lines
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 10*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var cur ipfsAddResponse
		if err := json.Unmarshal([]byte(line), &cur); err != nil {
			return "", err
		}
		if cur.Hash != "" {
			last = cur
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if last.Hash == "" {
		return "", fmt.Errorf("missing CID in IPFS response")
	}
	return last.Hash, nil
}

// withChiURLParam adds a URL parameter to the request context for testing
//
//nolint:unused // used in test files
func withChiURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
