package auth

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"vidra-core/internal/httpapi/shared"

	_ "github.com/HugoSmits86/nativewebp"
	"github.com/google/uuid"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/security"
	"vidra-core/internal/storage"
	"vidra-core/pkg/imageutil"
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
	testIPFSVerify     func(cid string) error
	testIPFSCat        func(cid string) (io.ReadCloser, error)
)

// UploadAvatar handles multipart upload of a user's avatar, uploads it to IPFS, pins it,
// and persists file_id + ipfs_cid in user_avatars.
func (h *AuthHandlers) UploadAvatar(w http.ResponseWriter, r *http.Request) {
	// Catch any panics
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("panic in UploadAvatar", "recovered", recovered)
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Internal server error"))
		}
	}()

	userID, _ := r.Context().Value(middleware.UserIDKey).(string)
	if userID == "" {
		slog.Warn("avatar upload: no user ID in context")
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
		return
	}

	// Parse and validate the uploaded file
	fileData, err := h.parseAvatarFile(r)
	if err != nil {
		slog.Error("avatar upload parse error", "user_id", userID, "error", err)
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	// Save file locally and generate WebP
	localPath, err := h.saveAvatarLocally(fileData)
	if err != nil {
		slog.Error("avatar save error", "user_id", userID, "error", err)
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	// Try to upload to IPFS if available
	var cid string
	var webpCID *string

	// Check if IPFS is configured
	if h.ipfsAPI != "" || h.ipfsClusterAPI != "" {
		// Upload to IPFS and pin
		cidResult, err := h.uploadAvatarToIPFS(localPath)
		if err != nil {
			// If IPFS is required, return error
			if h.cfg != nil && h.cfg.RequireIPFS {
				slog.Error("IPFS upload failed (required)", "user_id", userID, "error", err)
				shared.WriteError(w, http.StatusServiceUnavailable, err)
				return
			}
			// Otherwise, log warning and continue without IPFS
			slog.Warn("IPFS upload failed (optional), storing locally only", "user_id", userID, "error", err)
		} else {
			cid = cidResult
			// Verify the pinned content is retrievable (non-blocking: logs warning on failure)
			h.verifyIPFSContent(r.Context(), cid)
			// Upload WebP version if available
			webpCID = h.uploadWebPToIPFS(localPath)
		}
	}

	// Save to database - cid might be empty if IPFS is not available
	var ipfsNullString sql.NullString
	if cid != "" {
		ipfsNullString = sql.NullString{String: cid, Valid: true}
	}
	var webpNullString sql.NullString
	if webpCID != nil && *webpCID != "" {
		webpNullString = sql.NullString{String: *webpCID, Valid: true}
	}

	if err := h.userRepo.SetAvatarFields(r.Context(), userID, ipfsNullString, webpNullString); err != nil {
		slog.Error("failed to store avatar identifiers", "user_id", userID, "error", err)
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	// Return updated user
	user, err := h.userRepo.GetByID(r.Context(), userID)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	// Attach computed gateway URLs when IPFS CID is present
	if cid != "" && user.Avatar != nil && h.cfg != nil {
		if len(h.cfg.IPFSGatewayURLs) > 0 {
			user.Avatar.GatewayURL = h.cfg.IPFSGatewayURLs[0] + "/ipfs/" + cid
		}
		if h.cfg.IPFSLocalGatewayURL != "" {
			user.Avatar.LocalGatewayURL = h.cfg.IPFSLocalGatewayURL + "/ipfs/" + cid
		}
	}

	shared.WriteJSON(w, http.StatusOK, user)
}

// avatarFileData holds parsed file information
type avatarFileData struct {
	ext        string
	headBytes  []byte
	headSize   int
	fileReader io.Reader
}

// parseAvatarFile extracts and validates the uploaded file
func (h *AuthHandlers) parseAvatarFile(r *http.Request) (*avatarFileData, error) {
	// Basic form limit to avoid abuse (5MB)
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		return nil, domain.NewDomainError("BAD_REQUEST", "Failed to parse multipart form - missing or invalid data")
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, domain.NewDomainError("BAD_REQUEST", "Missing file field in form")
	}
	defer func() { _ = file.Close() }()

	// Determine extension early and validate
	ext := filepath.Ext(header.Filename)
	if !validAvatarExt(ext) {
		return nil, domain.NewDomainError("BAD_REQUEST", "unsupported file extension")
	}

	// Read the complete file content first
	fullContent, err := io.ReadAll(file)
	if err != nil {
		return nil, domain.NewDomainError("FILE_ERROR", "Failed to read file content")
	}

	// SECURITY FIX: Validate magic bytes to ensure file content matches extension
	// This prevents attackers from renaming malicious files to bypass extension checks
	if err := security.ValidateMagicBytes(fullContent, ext); err != nil {
		return nil, domain.NewDomainError("BAD_REQUEST", err.Error())
	}

	// MIME type sniffing from first 512 bytes
	contentType := http.DetectContentType(fullContent)

	if err := h.validateFileType(ext, contentType); err != nil {
		return nil, err
	}

	// Additional validation: try to decode the image to ensure it's valid
	testReader := bytes.NewReader(fullContent)
	if err := h.validateImageDecoding(testReader); err != nil {
		return nil, err
	}

	// Create a reader from the complete content
	reader := bytes.NewReader(fullContent)

	// Get first 512 bytes for head data
	headSize := 512
	if len(fullContent) < headSize {
		headSize = len(fullContent)
	}
	headBytes := make([]byte, headSize)
	copy(headBytes, fullContent[:headSize])

	return &avatarFileData{
		ext:        ext,
		headBytes:  headBytes,
		headSize:   headSize,
		fileReader: reader,
	}, nil
}

// validateFileType checks if the file type is allowed by attempting to decode it
func (h *AuthHandlers) validateFileType(ext, contentType string) error {
	// Common image extensions that should be supported
	allowedExts := []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".heic", ".heif", ".tiff", ".tif"}
	allowedByExt := false
	for _, allowedExt := range allowedExts {
		if strings.EqualFold(ext, allowedExt) {
			allowedByExt = true
			break
		}
	}

	// Check if content type indicates an image format
	allowedByMime := strings.HasPrefix(contentType, "image/")

	// Accept the file if either the extension or the MIME type suggests an image
	if !allowedByExt && !allowedByMime {
		return domain.NewDomainError("BAD_REQUEST", "Unsupported image format")
	}
	return nil
}

// validateImageDecoding attempts to decode the image to ensure it's a valid format
func (h *AuthHandlers) validateImageDecoding(r io.Reader) error {
	// Read first 32 bytes to check for special formats like HEIC
	var header [32]byte
	n, _ := r.Read(header[:])

	// Check for HEIC/HEIF files (they have 'ftyp' box at offset 4 and brand at offset 8)
	if n >= 12 && string(header[4:8]) == "ftyp" {
		brand := string(header[8:12])
		if brand == "heic" || brand == "heix" || brand == "heif" || brand == "mif1" {
			return nil // HEIC/HEIF files are valid
		}
	}

	// Check for TIFF files (II or MM magic bytes)
	if n >= 4 && ((header[0] == 'I' && header[1] == 'I' && header[2] == 0x2A && header[3] == 0x00) ||
		(header[0] == 'M' && header[1] == 'M' && header[2] == 0x00 && header[3] == 0x2A)) {
		return nil // TIFF files are valid
	}

	// For other formats, create a new reader with the header and try to decode
	fullReader := io.MultiReader(bytes.NewReader(header[:n]), r)
	_, _, err := image.DecodeConfig(fullReader)
	if err != nil {
		// If standard decoding fails, check if it might be a WebP file
		// WebP should be handled by the imported decoder, but be lenient
		if n >= 12 && string(header[0:4]) == "RIFF" && string(header[8:12]) == "WEBP" {
			return nil // WebP files are valid
		}
		return domain.NewDomainError("BAD_REQUEST", "Invalid or corrupted image file")
	}
	return nil
}

// saveAvatarLocally saves the file to local storage and generates WebP
func (h *AuthHandlers) saveAvatarLocally(fileData *avatarFileData) (string, error) {
	// Persist locally under storage/avatars via storage utility
	root := "./storage"
	if h.cfg != nil && h.cfg.StorageDir != "" {
		root = h.cfg.StorageDir
	}
	paths := storage.NewPaths(root)
	avatarsDir := paths.AvatarsDir()

	// Ensure the avatars directory exists
	if err := os.MkdirAll(avatarsDir, 0750); err != nil {
		return "", domain.NewDomainError("STORAGE_ERROR", fmt.Sprintf("Failed to create avatars directory %s: %v", avatarsDir, err))
	}

	// Generate an avatar ID used for filenames
	fileID := uuid.NewString()
	localPath := paths.AvatarFilePath(fileID, fileData.ext)

	// Validate path to prevent directory traversal
	if err := validateAvatarPath(localPath, root); err != nil {
		return "", domain.NewDomainError("INVALID_PATH", fmt.Sprintf("Invalid file path: %v", err))
	}
	// #nosec G304 - localPath validated against configured storage root
	out, err := os.Create(localPath)
	if err != nil {
		return "", domain.NewDomainError("STORAGE_ERROR", fmt.Sprintf("Failed to create file %s: %v", localPath, err))
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, fileData.fileReader); err != nil {
		return "", domain.NewDomainError("STORAGE_ERROR", fmt.Sprintf("Failed to write file data: %v", err))
	}

	// Generate WebP version
	h.generateWebP(localPath, paths.AvatarWebPPath(fileID))

	return localPath, nil
}

// generateWebP creates a WebP version of the image (best effort)
func (h *AuthHandlers) generateWebP(srcPath, dstPath string) {
	var encErr error
	if testEncodeToWebP != nil {
		encErr = testEncodeToWebP(srcPath, dstPath)
	} else {
		q := 0
		if h.cfg != nil {
			q = h.cfg.WebPQuality
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
func (h *AuthHandlers) uploadAvatarToIPFS(localPath string) (string, error) {
	// Check if IPFS is configured
	if h.ipfsAPI == "" && h.ipfsClusterAPI == "" {
		return "", domain.NewDomainError("IPFS_NOT_CONFIGURED", "IPFS is not configured")
	}

	// Upload to IPFS first
	var cid string
	var addErr error
	if testIPFSAdd != nil {
		cid, addErr = testIPFSAdd(localPath)
	} else if h.ipfsClusterAPI != "" {
		cid, addErr = h.ipfsClusterAdd(localPath)
	} else {
		cid, addErr = h.ipfsAdd(localPath)
	}
	if addErr != nil {
		return "", domain.NewDomainError("IPFS_UPLOAD_FAILED", "Failed to upload to IPFS")
	}

	// Pin the content
	if err := h.pinToIPFS(cid); err != nil {
		return "", err
	}

	return cid, nil
}

// verifyIPFSContent confirms the pinned content is retrievable from the local IPFS node.
// Non-blocking: logs a warning but does not fail the upload if verification fails.
func (h *AuthHandlers) verifyIPFSContent(ctx context.Context, cid string) {
	if testIPFSVerify != nil {
		if err := testIPFSVerify(cid); err != nil {
			slog.Warn("IPFS post-pin verification failed", "cid", cid, "error", err)
		}
		return
	}
	if h.ipfsAPI == "" {
		return
	}
	verifyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	u := h.ipfsAPI + "/api/v0/cat?arg=" + url.QueryEscape(cid)
	req, err := http.NewRequestWithContext(verifyCtx, http.MethodPost, u, nil)
	if err != nil {
		slog.Warn("IPFS verification: failed to build request", "cid", cid, "error", err)
		return
	}
	resp, err := ipfsHTTPClient.Do(req)
	if err != nil {
		slog.Warn("IPFS verification: request failed", "cid", cid, "error", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	// Read just 1 byte to confirm content is accessible
	buf := make([]byte, 1)
	if _, err := io.ReadAtLeast(resp.Body, buf, 1); err != nil {
		slog.Warn("IPFS verification: content not readable", "cid", cid, "error", err)
		return
	}
	slog.Info("IPFS post-pin verification succeeded", "cid", cid)
}

// pinToIPFS pins content to IPFS and optionally cluster
func (h *AuthHandlers) pinToIPFS(cid string) error {
	var pinErr error
	if testIPFSPin != nil {
		pinErr = testIPFSPin(cid)
	} else {
		pinErr = h.ipfsPin(cid)
	}
	if pinErr != nil {
		return domain.NewDomainError("IPFS_PIN_FAILED", "Failed to pin avatar in IPFS")
	}

	// Best-effort cluster pin
	if h.ipfsClusterAPI != "" {
		if testIPFSClusterPin != nil {
			_ = testIPFSClusterPin(cid)
		} else {
			_ = h.ipfsClusterPin(cid)
		}
	}
	return nil
}

// uploadWebPToIPFS uploads WebP version if it exists
func (h *AuthHandlers) uploadWebPToIPFS(originalPath string) *string {
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
		wcid, _ = h.ipfsAdd(webpPath)
	}
	if wcid == "" {
		return nil
	}

	// Pin best-effort
	_ = h.pinToIPFS(wcid)

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

func (h *AuthHandlers) ipfsAdd(path string) (string, error) {
	if h.ipfsAPI == "" {
		return "", fmt.Errorf("ipfs api not configured")
	}
	// Validate file path to prevent directory traversal
	root := "./storage"
	if h.cfg != nil && h.cfg.StorageDir != "" {
		root = h.cfg.StorageDir
	}
	if err := validateAvatarPath(path, root); err != nil {
		return "", fmt.Errorf("invalid file path: %w", err)
	}
	// #nosec G304 - path validated by validateAvatarPath prior to open
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
	req, err := http.NewRequest(http.MethodPost, h.ipfsAPI+"/api/v0/add?pin=true&cid-version=1&raw-leaves=true", &body)
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
func (h *AuthHandlers) ipfsClusterAdd(path string) (string, error) {
	if h.ipfsClusterAPI == "" {
		return "", fmt.Errorf("ipfs cluster api not configured")
	}
	// Validate file path to prevent directory traversal
	root := "./storage"
	if h.cfg != nil && h.cfg.StorageDir != "" {
		root = h.cfg.StorageDir
	}
	if err := validateAvatarPath(path, root); err != nil {
		return "", fmt.Errorf("invalid file path: %w", err)
	}
	// #nosec G304 - path validated by validateAvatarPath prior to open
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
	req, err := http.NewRequest(http.MethodPost, h.ipfsClusterAPI+"/add?cid-version=1&raw-leaves=true&pin=true", &body)
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
func (h *AuthHandlers) ipfsPin(cid string) error {
	if h.ipfsAPI == "" {
		return fmt.Errorf("ipfs api not configured")
	}
	u := h.ipfsAPI + "/api/v0/pin/add?arg=" + url.QueryEscape(cid)
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
func (h *AuthHandlers) ipfsClusterPin(cid string) error {
	if h.ipfsClusterAPI == "" {
		return nil
	}
	client := &http.Client{Timeout: 60 * time.Second}

	// Try Cluster v1 API: POST /pins/{cid}
	req, err := http.NewRequest(http.MethodPost, h.ipfsClusterAPI+"/pins/"+cid, nil)
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
		req2, err := http.NewRequest(http.MethodPost, h.ipfsClusterAPI+"/pins/add?arg="+url.QueryEscape(cid), nil)
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

// DeleteAvatar handles DELETE /api/v1/users/me/avatar.
// Clears the authenticated user's avatar by setting all avatar fields to null.
func (h *AuthHandlers) DeleteAvatar(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	nullStr := sql.NullString{Valid: false}
	if err := h.userRepo.SetAvatarFields(r.Context(), userID, nullStr, nullStr); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to clear avatar"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
