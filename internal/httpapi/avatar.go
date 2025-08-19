package httpapi

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "mime/multipart"
    "net/http"
    "os"
    "path/filepath"
    "time"

    "github.com/google/uuid"

    "athena/internal/domain"
    "athena/internal/middleware"
)

type ipfsAddResponse struct {
    Name string `json:"Name"`
    Hash string `json:"Hash"`
    Size string `json:"Size"`
}

// UploadAvatar handles multipart upload of a user's avatar, uploads it to IPFS, pins it,
// and persists file_id + ipfs_cid in user_avatars.
func (s *Server) UploadAvatar(w http.ResponseWriter, r *http.Request) {
    userID, _ := r.Context().Value(middleware.UserIDKey).(string)
    if userID == "" {
        WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
        return
    }
    // Basic form limit to avoid abuse (5MB)
    if err := r.ParseMultipartForm(5 << 20); err != nil {
        WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_MULTIPART", "Failed to parse form data"))
        return
    }
    file, header, err := r.FormFile("file")
    if err != nil {
        WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_FILE", "Missing file field in form"))
        return
    }
    defer file.Close()

    // Persist locally under uploads/avatars
    if err := os.MkdirAll("./uploads/avatars", 0755); err != nil {
        WriteError(w, http.StatusInternalServerError, domain.NewDomainError("STORAGE_ERROR", "Failed to prepare uploads directory"))
        return
    }
    fileID := uuid.NewString()
    ext := filepath.Ext(header.Filename)
    if len(ext) > 10 { // guard against weird long extensions
        ext = ""
    }
    localPath := filepath.Join("./uploads/avatars", fileID+ext)
    out, err := os.Create(localPath)
    if err != nil {
        WriteError(w, http.StatusInternalServerError, domain.NewDomainError("STORAGE_ERROR", "Failed to save file"))
        return
    }
    if _, err := io.Copy(out, file); err != nil {
        _ = out.Close()
        WriteError(w, http.StatusInternalServerError, domain.NewDomainError("STORAGE_ERROR", "Failed to write file"))
        return
    }
    _ = out.Close()

    // Upload to IPFS (Kubo API)
    cid, err := s.ipfsAdd(localPath)
    if err != nil {
        WriteError(w, http.StatusServiceUnavailable, domain.NewDomainError("IPFS_UPLOAD_FAILED", "Failed to upload to IPFS"))
        return
    }

    // Persist identifiers
    if err := s.userRepo.SetAvatarFields(r.Context(), userID, &fileID, &cid); err != nil {
        WriteError(w, http.StatusInternalServerError, domain.NewDomainError("DB_ERROR", "Failed to store avatar identifiers"))
        return
    }

    // Return updated user
    user, err := s.userRepo.GetByID(r.Context(), userID)
    if err != nil {
        WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to load user"))
        return
    }
    WriteJSON(w, http.StatusOK, user)
}

func (s *Server) ipfsAdd(path string) (string, error) {
    if s.ipfsAPI == "" {
        return "", fmt.Errorf("ipfs api not configured")
    }
    f, err := os.Open(path)
    if err != nil {
        return "", err
    }
    defer f.Close()
    var body bytes.Buffer
    mw := multipart.NewWriter(&body)
    fw, err := mw.CreateFormFile("file", filepath.Base(path))
    if err != nil {
        return "", err
    }
    if _, err := io.Copy(fw, f); err != nil {
        return "", err
    }
    _ = mw.Close()

    client := &http.Client{Timeout: 60 * time.Second}
    req, err := http.NewRequest(http.MethodPost, s.ipfsAPI+"/api/v0/add?pin=true", &body)
    if err != nil {
        return "", err
    }
    req.Header.Set("Content-Type", mw.FormDataContentType())

    resp, err := client.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
        return "", fmt.Errorf("ipfs add failed: %s", string(b))
    }

    // Kubo returns newline-delimited JSON if multiple files; we expect single entry.
    dec := json.NewDecoder(resp.Body)
    var last ipfsAddResponse
    for dec.More() {
        var cur ipfsAddResponse
        if err := dec.Decode(&cur); err != nil {
            return "", err
        }
        last = cur
    }
    if last.Hash == "" {
        // Handle case where it's a single object without dec.More() working
        if _, err := resp.Body.Seek(0, io.SeekStart); err == nil {
            _ = dec.Decode(&last)
        }
    }
    if last.Hash == "" {
        return "", fmt.Errorf("missing CID in IPFS response")
    }
    return last.Hash, nil
}

