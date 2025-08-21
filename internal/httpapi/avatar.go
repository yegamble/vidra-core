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
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"athena/internal/domain"
	"athena/internal/imageutil"
	"athena/internal/middleware"
	"athena/internal/storage"
)

type ipfsAddResponse struct {
	Name string `json:"Name"`
	Hash string `json:"Hash"`
	Size string `json:"Size"`
}

// Test hooks (override in tests to avoid real network calls)
var (
	testIPFSAdd        func(localPath string) (string, error)
	testIPFSPin        func(cid string) error
	testIPFSClusterPin func(cid string) error
	testEncodeToWebP   func(srcPath, dstPath string) error
)

// UploadAvatar handles multipart upload of a user's avatar, uploads it to IPFS, pins it,
// and persists file_id + ipfs_cid in user_avatars.
func (s *Server) UploadAvatar(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.UserIDKey).(string)
	if userID == "" {
		WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
		return
	}

	// Parse and validate the uploaded file
	fileData, err := s.parseAvatarFile(r)
	if err != nil {
		status := MapDomainErrorToHTTP(err)
		WriteError(w, status, err)
		return
	}

	// Save file locally and generate WebP
	localPath, err := s.saveAvatarLocally(fileData)
	if err != nil {
		status := MapDomainErrorToHTTP(err)
		WriteError(w, status, err)
		return
	}

	// Upload to IPFS and pin
	cid, err := s.uploadAvatarToIPFS(localPath)
	if err != nil {
		WriteError(w, http.StatusServiceUnavailable, err)
		return
	}

	// Upload WebP version if available
	webpCID := s.uploadWebPToIPFS(localPath)

	// Save to database
	if err := s.userRepo.SetAvatarFields(r.Context(), userID, &cid, webpCID); err != nil {
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

// avatarFileData holds parsed file information
type avatarFileData struct {
	ext        string
	headBytes  []byte
	headSize   int
	fileReader io.Reader
}

// parseAvatarFile extracts and validates the uploaded file
func (s *Server) parseAvatarFile(r *http.Request) (*avatarFileData, error) {
	// Basic form limit to avoid abuse (5MB)
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		return nil, domain.NewDomainError("INVALID_MULTIPART", "Failed to parse form data")
	}
	
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, domain.NewDomainError("MISSING_FILE", "Missing file field in form")
	}
	defer func() { _ = file.Close() }()

	// Determine extension early and validate
	ext := filepath.Ext(header.Filename)
	if !validAvatarExt(ext) {
		return nil, fmt.Errorf("invalid file extension: %w", domain.ErrBadRequest)
	}

	// MIME type sniffing from first 512 bytes
	var head [512]byte
	n, _ := file.Read(head[:])
	contentType := http.DetectContentType(head[:n])
	
	if err := s.validateFileType(ext, contentType); err != nil {
		return nil, err
	}

	// Reconstruct full reader: prepend sniffed bytes back before the remainder
	reader := io.MultiReader(bytes.NewReader(head[:n]), file)
	
	return &avatarFileData{
		ext:        ext,
		headBytes:  head[:n],
		headSize:   n,
		fileReader: reader,
	}, nil
}

// validateFileType checks if the file type is allowed
func (s *Server) validateFileType(ext, contentType string) error {
	allowedByExt := strings.EqualFold(ext, ".png") || strings.EqualFold(ext, ".jpg") || strings.EqualFold(ext, ".jpeg")
	allowedByMime := contentType == "image/png" || contentType == "image/jpeg"
	
	// Strict by default; allow extension-only fallback when ValidationTestMode is enabled
	if s.cfg != nil && s.cfg.ValidationTestMode {
		if !allowedByExt && !allowedByMime {
			return fmt.Errorf("only PNG and JPEG images are allowed: %w", domain.ErrBadRequest)
		}
	} else {
		if !allowedByMime {
			return fmt.Errorf("only PNG and JPEG images are allowed: %w", domain.ErrBadRequest)
		}
	}
	return nil
}

// saveAvatarLocally saves the file to local storage and generates WebP
func (s *Server) saveAvatarLocally(fileData *avatarFileData) (string, error) {
	// Persist locally under storage/avatars via storage utility
	root := "./storage"
	if s.cfg != nil && s.cfg.StorageDir != "" {
		root = s.cfg.StorageDir
	}
	paths := storage.NewPaths(root)
	if err := os.MkdirAll(paths.AvatarsDir(), 0750); err != nil {
		return "", domain.NewDomainError("STORAGE_ERROR", "Failed to prepare storage directory")
	}
	
	// Generate an avatar ID used for filenames
	fileID := uuid.NewString()
	localPath := paths.AvatarFilePath(fileID, fileData.ext)
	
	// Validate path to prevent directory traversal
	if err := validateAvatarPath(localPath, root); err != nil {
		return "", domain.NewDomainError("INVALID_PATH", "Invalid file path")
	}
	out, err := os.Create(localPath)
	if err != nil {
		return "", domain.NewDomainError("STORAGE_ERROR", "Failed to save file")
	}
	defer func() { _ = out.Close() }()
	
	if _, err := io.Copy(out, fileData.fileReader); err != nil {
		return "", domain.NewDomainError("STORAGE_ERROR", "Failed to write file")
	}

	// Generate WebP version
	s.generateWebP(localPath, paths.AvatarWebPPath(fileID))
	
	return localPath, nil
}

// generateWebP creates a WebP version of the image (best effort)
func (s *Server) generateWebP(srcPath, dstPath string) {
	var encErr error
	if testEncodeToWebP != nil {
		encErr = testEncodeToWebP(srcPath, dstPath)
	} else {
		q := 0
		if s.cfg != nil {
			q = s.cfg.WebPQuality
		}
		if q > 0 {
			encErr = imageutil.EncodeFileToWebPWithQuality(srcPath, dstPath, q)
		} else {
			encErr = imageutil.EncodeFileToWebP(srcPath, dstPath)
		}
	}
	if encErr != nil && encErr != imageutil.ErrWebPUnavailable {
		// Non-fatal; continue with original avatar
		_ = encErr
	}
}

// uploadAvatarToIPFS uploads and pins the avatar to IPFS
func (s *Server) uploadAvatarToIPFS(localPath string) (string, error) {
	// Upload to IPFS first
	var cid string
	var addErr error
	if testIPFSAdd != nil {
		cid, addErr = testIPFSAdd(localPath)
	} else if s.ipfsClusterAPI != "" {
		cid, addErr = s.ipfsClusterAdd(localPath)
	} else {
		cid, addErr = s.ipfsAdd(localPath)
	}
	if addErr != nil {
		return "", domain.NewDomainError("IPFS_UPLOAD_FAILED", "Failed to upload to IPFS")
	}

	// Pin the content
	if err := s.pinToIPFS(cid); err != nil {
		return "", err
	}

	return cid, nil
}

// pinToIPFS pins content to IPFS and optionally cluster
func (s *Server) pinToIPFS(cid string) error {
	var pinErr error
	if testIPFSPin != nil {
		pinErr = testIPFSPin(cid)
	} else {
		pinErr = s.ipfsPin(cid)
	}
	if pinErr != nil {
		return domain.NewDomainError("IPFS_PIN_FAILED", "Failed to pin avatar in IPFS")
	}
	
	// Best-effort cluster pin
	if s.ipfsClusterAPI != "" {
		if testIPFSClusterPin != nil {
			_ = testIPFSClusterPin(cid)
		} else {
			_ = s.ipfsClusterPin(cid)
		}
	}
	return nil
}

// uploadWebPToIPFS uploads WebP version if it exists
func (s *Server) uploadWebPToIPFS(originalPath string) *string {
	// Derive WebP path from original
	paths := storage.NewPaths(filepath.Dir(filepath.Dir(originalPath)))
	fileID := strings.TrimSuffix(filepath.Base(originalPath), filepath.Ext(originalPath))
	webpPath := paths.AvatarWebPPath(fileID)
	
	if _, err := os.Stat(webpPath); err != nil {
		return nil // WebP doesn't exist
	}

	var wcid string
	if testIPFSAdd != nil {
		wcid, _ = testIPFSAdd(webpPath)
	} else {
		wcid, _ = s.ipfsAdd(webpPath)
	}
	if wcid == "" {
		return nil
	}

	// Pin best-effort
	_ = s.pinToIPFS(wcid)
	
	return &wcid
}

// validAvatarExt rejects path separators and excessively long extensions.
// Empty extension is allowed (saved without extension).
var avatarExtRe = regexp.MustCompile(`^\.[A-Za-z0-9]{1,8}$`)

func validAvatarExt(ext string) bool {
	if ext == "" { // allow files without extension
		return true
	}
	// Strictly allow only .[A-Za-z0-9]{1,8}
	return avatarExtRe.MatchString(ext)
}

// validateAvatarPath ensures the avatar path is within expected boundaries
func validateAvatarPath(path, expectedRoot string) error {
	// Clean the path to resolve any ../ or ./ elements
	cleanPath := filepath.Clean(path)
	
	// Ensure the path is absolute or make it relative to expected root
	if !filepath.IsAbs(cleanPath) {
		cleanPath = filepath.Join(expectedRoot, cleanPath)
	}
	
	// Check if the resolved path is within the expected root
	if expectedRoot != "" {
		expectedRoot = filepath.Clean(expectedRoot)
		rel, err := filepath.Rel(expectedRoot, cleanPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("path traversal detected: %s", path)
		}
	}
	
	// Additional security checks
	if strings.Contains(cleanPath, "..") {
		return fmt.Errorf("path contains directory traversal: %s", path)
	}
	
	return nil
}

func (s *Server) ipfsAdd(path string) (string, error) {
	if s.ipfsAPI == "" {
		return "", fmt.Errorf("ipfs api not configured")
	}
	// Validate file path to prevent directory traversal
	root := "./storage"
	if s.cfg != nil && s.cfg.StorageDir != "" {
		root = s.cfg.StorageDir
	}
	if err := validateAvatarPath(path, root); err != nil {
		return "", fmt.Errorf("invalid file path: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
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
	defer func() { _ = resp.Body.Close() }()
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
	// Validate file path to prevent directory traversal
	root := "./storage"
	if s.cfg != nil && s.cfg.StorageDir != "" {
		root = s.cfg.StorageDir
	}
	if err := validateAvatarPath(path, root); err != nil {
		return "", fmt.Errorf("invalid file path: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
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
	defer func() { _ = resp.Body.Close() }()
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
	defer func() { _ = resp.Body.Close() }()
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
	defer func() { _ = resp.Body.Close() }()
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
		defer func() { _ = resp2.Body.Close() }()
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
