package httpapi

import (
    "bufio"
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "mime/multipart"
    "net/http"
    "net/url"
    "os"
    "path/filepath"
    "strings"
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

    // Upload to IPFS first (to ensure content is available), then pin
    var cid string
    var addErr error
    if s.ipfsClusterAPI != "" {
        // Prefer Cluster add if configured to ensure cluster-aware ingestion
        cid, addErr = s.ipfsClusterAdd(localPath)
    } else {
        cid, addErr = s.ipfsAdd(localPath)
    }
    if addErr != nil {
        WriteError(w, http.StatusServiceUnavailable, domain.NewDomainError("IPFS_UPLOAD_FAILED", "Failed to upload to IPFS"))
        return
    }

    // Ensure pinned in Kubo and optionally in IPFS Cluster
    if pinErr := s.ipfsPin(cid); pinErr != nil {
        // If pinning fails after add, treat as service unavailable
        WriteError(w, http.StatusServiceUnavailable, domain.NewDomainError("IPFS_PIN_FAILED", "Failed to pin avatar in IPFS"))
        return
    }
    if s.ipfsClusterAPI != "" {
        _ = s.ipfsClusterPin(cid) // Best-effort cluster pin (add already pinned on cluster)
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
    // Request pin on add and use CIDv1 for consistency
    req, err := http.NewRequest(http.MethodPost, s.ipfsAPI+"/api/v0/add?pin=true&cid-version=1&raw-leaves=true", &body)
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

    // Kubo returns NDJSON (one JSON object per line). Read body and decode last object safely.
    cid, err := parseIPFSAddResponse(resp.Body)
    if err != nil {
        return "", err
    }
    return cid, nil
}

// ipfsClusterAdd uploads a file via IPFS Cluster's /add endpoint (streaming NDJSON), returning the final CID.
func (s *Server) ipfsClusterAdd(path string) (string, error) {
    if s.ipfsClusterAPI == "" {
        return "", fmt.Errorf("ipfs cluster api not configured")
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

    client := &http.Client{Timeout: 120 * time.Second}
    // Cluster add typically mirrors Kubo's add query params
    req, err := http.NewRequest(http.MethodPost, s.ipfsClusterAPI+"/add?cid-version=1&raw-leaves=true&pin=true", &body)
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
        return "", fmt.Errorf("ipfs cluster add failed: %s", string(b))
    }
    return parseIPFSAddResponse(resp.Body)
}

// parseIPFSAddResponse parses the final CID from an ipfs add NDJSON stream.
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

// ipfsPin ensures the CID is pinned on the local Kubo node (idempotent).
func (s *Server) ipfsPin(cid string) error {
    if s.ipfsAPI == "" {
        return fmt.Errorf("ipfs api not configured")
    }
    u := s.ipfsAPI + "/api/v0/pin/add?arg=" + url.QueryEscape(cid)
    client := &http.Client{Timeout: 60 * time.Second}
    req, err := http.NewRequest(http.MethodPost, u, nil)
    if err != nil {
        return err
    }
    resp, err := client.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode >= 200 && resp.StatusCode < 300 {
        return nil
    }
    b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
    return fmt.Errorf("ipfs pin add failed: %s", string(b))
}

// ipfsClusterPin best-effort pin to IPFS Cluster if configured.
func (s *Server) ipfsClusterPin(cid string) error {
    if s.ipfsClusterAPI == "" {
        return nil
    }
    client := &http.Client{Timeout: 60 * time.Second}

    // Try Cluster v1 API: POST /pins/{cid}
    req, err := http.NewRequest(http.MethodPost, s.ipfsClusterAPI+"/pins/"+cid, nil)
    if err != nil {
        return err
    }
    resp, err := client.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode >= 200 && resp.StatusCode < 300 {
        return nil
    }
    if resp.StatusCode == http.StatusConflict { // already pinned
        return nil
    }

    // Fallback to older Cluster API: POST /pins/add?arg=cid
    if resp.StatusCode == http.StatusNotFound {
        req2, err := http.NewRequest(http.MethodPost, s.ipfsClusterAPI+"/pins/add?arg="+url.QueryEscape(cid), nil)
        if err != nil {
            return err
        }
        resp2, err := client.Do(req2)
        if err != nil {
            return err
        }
        defer resp2.Body.Close()
        if resp2.StatusCode >= 200 && resp2.StatusCode < 300 {
            return nil
        }
        if resp2.StatusCode == http.StatusConflict {
            return nil
        }
        b, _ := io.ReadAll(io.LimitReader(resp2.Body, 2048))
        return fmt.Errorf("ipfs cluster pin failed: %s", string(b))
    }

    b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
    return fmt.Errorf("ipfs cluster pin failed: %s", string(b))
}
