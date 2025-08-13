package httpapi

import (
    "bytes"
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/go-chi/chi/v5"

    "athena/internal/domain"
    "athena/internal/repository"
    "athena/internal/testutil"
)

// decode helper for API response wrapper
type integResp struct {
    Data    json.RawMessage `json:"data"`
    Error   *ErrorInfo      `json:"error"`
    Success bool            `json:"success"`
    Meta    *Meta           `json:"meta"`
}

func decodeInteg(rr *httptest.ResponseRecorder, t *testing.T) integResp {
    t.Helper()
    var resp integResp
    if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
        t.Fatalf("failed to decode response: %v", err)
    }
    return resp
}

func TestRegister_Integration_CreatesUserInDB(t *testing.T) {
    td := testutil.SetupTestDB(t)
    td.TruncateTables(t, "users", "refresh_tokens", "sessions")

    repo := repository.NewUserRepository(td.DB)
    s := NewServer(repo, repository.NewAuthRepository(td.DB))

    // unique values
    uname := "reg_" + time.Now().Format("20060102150405")
    email := uname + "@example.com"

    body := map[string]any{
        "username":     uname,
        "email":        email,
        "password":     "integration-password-12345",
        "display_name": "Reg Integ",
    }
    b, _ := json.Marshal(body)
    req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(b))
    req.Header.Set("Content-Type", "application/json")
    rr := httptest.NewRecorder()

    s.Register(rr, req)

    if rr.Code != http.StatusCreated {
        t.Fatalf("expected 201, got %d, body=%s", rr.Code, rr.Body.String())
    }

    // Verify response and DB
    resp := decodeInteg(rr, t)
    if !resp.Success {
        t.Fatalf("expected success=true")
    }
    // DB has created user
    got, err := repo.GetByEmail(context.Background(), email)
    if err != nil {
        t.Fatalf("expected user in DB: %v", err)
    }
    if got.Username != uname || got.Email != email {
        t.Fatalf("unexpected stored user: %+v", got)
    }
}

func TestCreateUserHandler_Integration_CreatesUserInDB(t *testing.T) {
    td := testutil.SetupTestDB(t)
    td.TruncateTables(t, "users")

    repo := repository.NewUserRepository(td.DB)
    h := CreateUserHandler(repo)

    uname := "api_" + time.Now().Format("20060102150405")
    email := uname + "@example.com"

    body := map[string]any{
        "username":     uname,
        "email":        email,
        "password":     "integration-password-abc",
        "display_name": "API Integ",
    }
    b, _ := json.Marshal(body)
    req := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewReader(b))
    req.Header.Set("Content-Type", "application/json")
    rr := httptest.NewRecorder()

    h.ServeHTTP(rr, req)

    if rr.Code != http.StatusCreated {
        t.Fatalf("expected 201, got %d, body=%s", rr.Code, rr.Body.String())
    }

    // Confirm persisted
    got, err := repo.GetByEmail(context.Background(), email)
    if err != nil {
        t.Fatalf("expected user in DB: %v", err)
    }
    if got.Username != uname {
        t.Fatalf("unexpected stored user: %+v", got)
    }
}

func TestGetUserHandler_Integration_GetsFromDB(t *testing.T) {
    td := testutil.SetupTestDB(t)
    td.TruncateTables(t, "users")

    repo := repository.NewUserRepository(td.DB)

    // Seed a user directly via repo
    u := &domain.User{
        ID:          "u_" + time.Now().Format("20060102150405"),
        Username:    "get_integ",
        Email:       "get_integ@example.com",
        DisplayName: "Getter",
        Role:        domain.RoleUser,
        IsActive:    true,
        CreatedAt:   time.Now(),
        UpdatedAt:   time.Now(),
    }
    if err := repo.Create(context.Background(), u, "hash"); err != nil {
        t.Fatalf("seed create failed: %v", err)
    }

    r := chi.NewRouter()
    r.Get("/{id}", GetUserHandler(repo))

    req := httptest.NewRequest(http.MethodGet, "/"+u.ID, nil)
    rr := httptest.NewRecorder()
    r.ServeHTTP(rr, req)

    if rr.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d, body=%s", rr.Code, rr.Body.String())
    }

    resp := decodeInteg(rr, t)
    var got domain.User
    if err := json.Unmarshal(resp.Data, &got); err != nil {
        t.Fatalf("unmarshal user: %v", err)
    }
    if got.ID != u.ID || got.Username != u.Username {
        t.Fatalf("unexpected user: %+v", got)
    }
}
