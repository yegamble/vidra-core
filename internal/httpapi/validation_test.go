package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// sampleRequest is a test DTO exercising the Validatable contract.
type sampleRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (r sampleRequest) Validate() []FieldError {
	var fes []FieldError
	if strings.TrimSpace(r.Name) == "" {
		fes = append(fes, FieldError{Field: "name", Message: "is required"})
	}
	if !strings.Contains(r.Email, "@") {
		fes = append(fes, FieldError{Field: "email", Message: "must be a valid email"})
	}
	return fes
}

// mount a handler that binds+validates and echoes 200 on success.
func validatingServer(t *testing.T) *Server {
	t.Helper()
	srv := New(testConfig(), nil, nil)
	srv.echo.POST("/v", func(c echo.Context) error {
		var in sampleRequest
		if err := bindAndValidate(c, &in); err != nil {
			return err
		}
		return c.JSON(http.StatusOK, in)
	})
	return srv
}

func postJSON(srv *Server, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestBindAndValidateAcceptsValid(t *testing.T) {
	rec := postJSON(validatingServer(t), `{"name":"Ada","email":"ada@example.test"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestBindAndValidateRejectsMalformedJSON(t *testing.T) {
	rec := postJSON(validatingServer(t), `{"name":`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error.Code != "bad_request" {
		t.Errorf("code = %q, want bad_request", body.Error.Code)
	}
}

func TestBindAndValidateReportsFieldErrors(t *testing.T) {
	rec := postJSON(validatingServer(t), `{"name":"","email":"nope"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error.Code != "unprocessable_entity" {
		t.Errorf("code = %q, want unprocessable_entity", body.Error.Code)
	}
	if len(body.Error.Fields) != 2 {
		t.Fatalf("fields = %+v, want 2 entries", body.Error.Fields)
	}
	got := map[string]string{}
	for _, f := range body.Error.Fields {
		got[f.Field] = f.Message
	}
	if got["name"] == "" || got["email"] == "" {
		t.Errorf("expected name and email field errors, got %+v", body.Error.Fields)
	}
}
