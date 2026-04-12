package video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/repository"
	"vidra-core/internal/storage"
	"vidra-core/internal/testutil"
	"vidra-core/internal/usecase"

	chi "github.com/go-chi/chi/v5"
)

// writeMinimalMP4 creates a tiny, valid-looking MP4 file with an ftyp and mdat box.
// It doesn't need to be playable for our upload pipeline; we only verify bytes round-trip.
func writeMinimalMP4(path string, totalSize int) error {
	// Construct a minimal MP4: [ftyp box][mdat box + payload]
	// ftyp box: 24 bytes: size(4) + 'ftyp'(4) + major_brand(4) + minor_version(4) + compatible_brands(8)
	ftyp := make([]byte, 24)
	binary.BigEndian.PutUint32(ftyp[0:4], uint32(24))
	copy(ftyp[4:8], []byte("ftyp"))
	copy(ftyp[8:12], []byte("isom"))
	binary.BigEndian.PutUint32(ftyp[12:16], 0)
	copy(ftyp[16:20], []byte("isom"))
	copy(ftyp[20:24], []byte("iso2"))

	// Remaining size for mdat box
	if totalSize < len(ftyp)+8 {
		totalSize = len(ftyp) + 8
	}
	payloadSize := totalSize - len(ftyp) - 8 // mdat header is 8 bytes

	// mdat header
	mdat := make([]byte, 8)
	binary.BigEndian.PutUint32(mdat[0:4], uint32(payloadSize+8))
	copy(mdat[4:8], []byte("mdat"))

	// Payload with deterministic pattern
	payload := bytes.Repeat([]byte{0x11, 0x22, 0x33, 0x44}, (payloadSize+3)/4)
	payload = payload[:payloadSize]

	// Write to file
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(ftyp); err != nil {
		return err
	}
	if _, err := f.Write(mdat); err != nil {
		return err
	}
	if _, err := f.Write(payload); err != nil {
		return err
	}
	return nil
}

func TestUploadWithActualVideoFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}
	testDB := testutil.SetupTestDB(t)
	if testDB == nil { // skipped
		return
	}
	uploadRepo := repository.NewUploadRepository(testDB.DB)
	encodingRepo := repository.NewEncodingRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)

	tempDir := t.TempDir()
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, tempDir, createTestConfig())

	ctx := context.Background()

	// Create a tiny mp4 file in testdata
	sampleDir := filepath.Join(tempDir, "testdata")
	samplePath := filepath.Join(sampleDir, "sample.mp4")
	// ~128KB file
	if err := writeMinimalMP4(samplePath, 128*1024); err != nil {
		t.Fatalf("failed to create sample mp4: %v", err)
	}

	fileBytes, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatalf("failed to read sample mp4: %v", err)
	}

	// Create test user
	user := createTestUser(t, userRepo, ctx, "videouser", "video@example.com")

	// Step 1: Initiate upload via handler
	chunkSize := int64(16 * 1024) // 16KB chunks
	req := domain.InitiateUploadRequest{
		FileName:  "sample.mp4",
		FileSize:  int64(len(fileBytes)),
		ChunkSize: chunkSize,
	}
	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/api/v1/uploads/initiate", bytes.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), middleware.UserIDKey, user.ID))

	w := httptest.NewRecorder()
	handler := InitiateUploadHandler(uploadService, videoRepo)
	handler(w, httpReq)
	if w.Code != http.StatusCreated {
		t.Fatalf("initiate failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var env Response
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !env.Success {
		t.Fatalf("unexpected error: %+v", env.Error)
	}

	var initResp domain.InitiateUploadResponse
	dataBytes, _ := json.Marshal(env.Data)
	if err := json.Unmarshal(dataBytes, &initResp); err != nil {
		t.Fatal(err)
	}

	sessionID := initResp.SessionID

	// Step 2: Upload all chunks
	totalChunks := (len(fileBytes) + int(chunkSize) - 1) / int(chunkSize)
	for i := 0; i < totalChunks; i++ {
		start := i * int(chunkSize)
		end := start + int(chunkSize)
		if end > len(fileBytes) {
			end = len(fileBytes)
		}
		chunk := fileBytes[start:end]

		hasher := sha256.New()
		hasher.Write(chunk)
		checksum := hex.EncodeToString(hasher.Sum(nil))

		httpReq = httptest.NewRequest("POST", "/api/v1/uploads/"+sessionID+"/chunks", bytes.NewReader(chunk))
		httpReq.Header.Set("X-Chunk-Index", strconv.Itoa(i))
		httpReq.Header.Set("X-Chunk-Checksum", checksum)

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("sessionId", sessionID)
		httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))

		w = httptest.NewRecorder()
		upHandler := UploadChunkHandler(uploadService, createTestConfig())
		upHandler(w, httpReq)
		if w.Code != http.StatusOK {
			t.Fatalf("upload chunk %d failed: code=%d body=%s", i, w.Code, w.Body.String())
		}
		// Sanity parse
		var env2 Response
		_ = json.Unmarshal(w.Body.Bytes(), &env2)
		if !env2.Success {
			t.Fatalf("chunk error: %+v", env2.Error)
		}
	}

	// Step 3: Complete upload
	httpReq = httptest.NewRequest("POST", "/api/v1/uploads/"+sessionID+"/complete", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sessionId", sessionID)
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))
	w = httptest.NewRecorder()
	compHandler := CompleteUploadHandler(uploadService, encodingRepo, nil)
	compHandler(w, httpReq)
	if w.Code != http.StatusOK {
		t.Fatalf("complete failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// Verify assembled file exists and matches original
	session, err := uploadRepo.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	sp := storage.NewPaths(tempDir)
	assembledPath := sp.WebVideoFilePath(session.VideoID, filepath.Ext(req.FileName))
	assembledBytes, err := os.ReadFile(assembledPath)
	if err != nil {
		t.Fatalf("read assembled: %v", err)
	}

	if !bytes.Equal(fileBytes, assembledBytes) {
		t.Fatalf("assembled file does not match original: got %d want %d bytes", len(assembledBytes), len(fileBytes))
	}

	// Also stream back a bit of the file to ensure it's readable
	f, err := os.Open(assembledPath)
	if err != nil {
		t.Fatalf("open assembled: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.CopyN(io.Discard, f, 64); err != nil && err != io.EOF {
		t.Fatalf("read assembled content: %v", err)
	}
}

func TestUploadLargeVideo_VariousChunkSizes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}
	testDB := testutil.SetupTestDB(t)
	if testDB == nil { // skipped
		return
	}
	uploadRepo := repository.NewUploadRepository(testDB.DB)
	encodingRepo := repository.NewEncodingRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)

	tempDir := t.TempDir()
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, tempDir, createTestConfig())

	ctx := context.Background()
	user := createTestUser(t, userRepo, ctx, "videouser2", "video2@example.com")

	// Prepare a ~1 MiB MP4 to keep runtime reasonable
	sampleDir := filepath.Join(tempDir, "testdata")
	samplePath := filepath.Join(sampleDir, "sample_large.mp4")
	if err := writeMinimalMP4(samplePath, 1024*1024); err != nil {
		t.Fatalf("failed to create sample mp4: %v", err)
	}
	fileBytes, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}

	chunkSizes := []int64{4 * 1024, 16 * 1024, 64 * 1024, 128 * 1024}
	for _, chunkSize := range chunkSizes {
		// Initiate
		req := domain.InitiateUploadRequest{FileName: "sample_large.mp4", FileSize: int64(len(fileBytes)), ChunkSize: chunkSize}
		reqBody, _ := json.Marshal(req)
		httpReq := httptest.NewRequest("POST", "/api/v1/uploads/initiate", bytes.NewReader(reqBody))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), middleware.UserIDKey, user.ID))
		w := httptest.NewRecorder()
		InitiateUploadHandler(uploadService, videoRepo)(w, httpReq)
		if w.Code != http.StatusCreated {
			t.Fatalf("initiate failed chunkSize=%d: code=%d body=%s", chunkSize, w.Code, w.Body.String())
		}
		var env Response
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if !env.Success {
			t.Fatalf("init env error: %+v", env.Error)
		}
		var initResp domain.InitiateUploadResponse
		dataBytes, _ := json.Marshal(env.Data)
		if err := json.Unmarshal(dataBytes, &initResp); err != nil {
			t.Fatal(err)
		}

		// Upload all chunks
		totalChunks := (len(fileBytes) + int(chunkSize) - 1) / int(chunkSize)
		for i := 0; i < totalChunks; i++ {
			start := i * int(chunkSize)
			end := start + int(chunkSize)
			if end > len(fileBytes) {
				end = len(fileBytes)
			}
			chunk := fileBytes[start:end]
			h := sha256.New()
			h.Write(chunk)
			checksum := hex.EncodeToString(h.Sum(nil))

			httpReq = httptest.NewRequest("POST", "/api/v1/uploads/"+initResp.SessionID+"/chunks", bytes.NewReader(chunk))
			httpReq.Header.Set("X-Chunk-Index", strconv.Itoa(i))
			httpReq.Header.Set("X-Chunk-Checksum", checksum)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("sessionId", initResp.SessionID)
			httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))
			w = httptest.NewRecorder()
			UploadChunkHandler(uploadService, createTestConfig())(w, httpReq)
			if w.Code != http.StatusOK {
				t.Fatalf("upload failed chunkSize=%d i=%d: code=%d body=%s", chunkSize, i, w.Code, w.Body.String())
			}
		}

		// Complete
		httpReq = httptest.NewRequest("POST", "/api/v1/uploads/"+initResp.SessionID+"/complete", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("sessionId", initResp.SessionID)
		httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))
		w = httptest.NewRecorder()
		CompleteUploadHandler(uploadService, encodingRepo, nil)(w, httpReq)
		if w.Code != http.StatusOK {
			t.Fatalf("complete failed chunkSize=%d: code=%d body=%s", chunkSize, w.Code, w.Body.String())
		}

		// Compare output
		session, err := uploadRepo.GetSession(ctx, initResp.SessionID)
		if err != nil {
			t.Fatalf("get session: %v", err)
		}
		sp := storage.NewPaths(tempDir)
		assembledPath := sp.WebVideoFilePath(session.VideoID, filepath.Ext(req.FileName))
		out, err := os.ReadFile(assembledPath)
		if err != nil {
			t.Fatalf("read assembled: %v", err)
		}
		if !bytes.Equal(fileBytes, out) {
			t.Fatalf("mismatch for chunkSize=%d: got %d want %d", chunkSize, len(out), len(fileBytes))
		}
	}
}

func TestResumeUploadWithActualVideoFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}
	testDB := testutil.SetupTestDB(t)
	if testDB == nil { // skipped
		return
	}
	uploadRepo := repository.NewUploadRepository(testDB.DB)
	encodingRepo := repository.NewEncodingRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)

	tempDir := t.TempDir()
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, tempDir, createTestConfig())

	ctx := context.Background()
	user := createTestUser(t, userRepo, ctx, "videouser3", "video3@example.com")

	// ~256 KiB file
	sampleDir := filepath.Join(tempDir, "testdata")
	samplePath := filepath.Join(sampleDir, "sample_resume.mp4")
	if err := writeMinimalMP4(samplePath, 256*1024); err != nil {
		t.Fatalf("failed to create sample mp4: %v", err)
	}
	fileBytes, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}

	chunkSize := int64(16 * 1024) // 16KB
	// Initiate
	req := domain.InitiateUploadRequest{FileName: "sample_resume.mp4", FileSize: int64(len(fileBytes)), ChunkSize: chunkSize}
	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/api/v1/uploads/initiate", bytes.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), middleware.UserIDKey, user.ID))
	w := httptest.NewRecorder()
	InitiateUploadHandler(uploadService, videoRepo)(w, httpReq)
	if w.Code != http.StatusCreated {
		t.Fatalf("initiate failed: %d %s", w.Code, w.Body.String())
	}
	var env Response
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !env.Success {
		t.Fatalf("unexpected error: %+v", env.Error)
	}
	var initResp domain.InitiateUploadResponse
	dataBytes, _ := json.Marshal(env.Data)
	if err := json.Unmarshal(dataBytes, &initResp); err != nil {
		t.Fatal(err)
	}

	totalChunks := (len(fileBytes) + int(chunkSize) - 1) / int(chunkSize)

	// Upload first 5 chunks
	uploaded := map[int]bool{}
	for i := 0; i < totalChunks && i < 5; i++ {
		start := i * int(chunkSize)
		end := start + int(chunkSize)
		if end > len(fileBytes) {
			end = len(fileBytes)
		}
		chunk := fileBytes[start:end]
		h := sha256.New()
		h.Write(chunk)
		checksum := hex.EncodeToString(h.Sum(nil))
		httpReq = httptest.NewRequest("POST", "/api/v1/uploads/"+initResp.SessionID+"/chunks", bytes.NewReader(chunk))
		httpReq.Header.Set("X-Chunk-Index", strconv.Itoa(i))
		httpReq.Header.Set("X-Chunk-Checksum", checksum)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("sessionId", initResp.SessionID)
		httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))
		w = httptest.NewRecorder()
		UploadChunkHandler(uploadService, createTestConfig())(w, httpReq)
		if w.Code != http.StatusOK {
			t.Fatalf("upload i=%d failed: %d %s", i, w.Code, w.Body.String())
		}
		uploaded[i] = true
	}

	// Call resume
	httpReq = httptest.NewRequest("GET", "/api/v1/uploads/"+initResp.SessionID+"/resume", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sessionId", initResp.SessionID)
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))
	w = httptest.NewRecorder()
	ResumeUploadHandler(uploadService)(w, httpReq)
	if w.Code != http.StatusOK {
		t.Fatalf("resume failed: %d %s", w.Code, w.Body.String())
	}
	var env2 Response
	if err := json.Unmarshal(w.Body.Bytes(), &env2); err != nil {
		t.Fatal(err)
	}
	if !env2.Success {
		t.Fatalf("resume error: %+v", env2.Error)
	}
	var resumeMap map[string]interface{}
	dataBytes2, _ := json.Marshal(env2.Data)
	if err := json.Unmarshal(dataBytes2, &resumeMap); err != nil {
		t.Fatal(err)
	}
	// Basic checks
	if resumeMap["session_id"].(string) != initResp.SessionID {
		t.Fatal("session mismatch")
	}

	// Upload remaining chunks in non-sequential order
	for i := totalChunks - 1; i >= 0; i-- { // reverse order to mix things up
		if uploaded[i] {
			continue
		}
		start := i * int(chunkSize)
		end := start + int(chunkSize)
		if end > len(fileBytes) {
			end = len(fileBytes)
		}
		chunk := fileBytes[start:end]
		h := sha256.New()
		h.Write(chunk)
		checksum := hex.EncodeToString(h.Sum(nil))
		httpReq = httptest.NewRequest("POST", "/api/v1/uploads/"+initResp.SessionID+"/chunks", bytes.NewReader(chunk))
		httpReq.Header.Set("X-Chunk-Index", strconv.Itoa(i))
		httpReq.Header.Set("X-Chunk-Checksum", checksum)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("sessionId", initResp.SessionID)
		httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))
		w = httptest.NewRecorder()
		UploadChunkHandler(uploadService, createTestConfig())(w, httpReq)
		if w.Code != http.StatusOK {
			t.Fatalf("upload remaining i=%d failed: %d %s", i, w.Code, w.Body.String())
		}
	}

	// Complete
	httpReq = httptest.NewRequest("POST", "/api/v1/uploads/"+initResp.SessionID+"/complete", nil)
	rctx = chi.NewRouteContext()
	rctx.URLParams.Add("sessionId", initResp.SessionID)
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))
	w = httptest.NewRecorder()
	CompleteUploadHandler(uploadService, encodingRepo, nil)(w, httpReq)
	if w.Code != http.StatusOK {
		t.Fatalf("complete failed: %d %s", w.Code, w.Body.String())
	}

	// Validate assembled file
	session, err := uploadRepo.GetSession(ctx, initResp.SessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	sp := storage.NewPaths(tempDir)
	assembledPath := sp.WebVideoFilePath(session.VideoID, filepath.Ext(req.FileName))
	out, err := os.ReadFile(assembledPath)
	if err != nil {
		t.Fatalf("read assembled: %v", err)
	}
	if !bytes.Equal(fileBytes, out) {
		t.Fatalf("assembled mismatch: got %d want %d", len(out), len(fileBytes))
	}
}

func TestUploadResumeAfterChecksumMismatch_WithVideoFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}
	testDB := testutil.SetupTestDB(t)
	if testDB == nil { // skipped
		return
	}
	uploadRepo := repository.NewUploadRepository(testDB.DB)
	encodingRepo := repository.NewEncodingRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)

	tempDir := t.TempDir()
	// Use relaxed config (strict=false) so only mismatched checksum causes 400, not missing
	cfg := &config.Config{ValidationStrictMode: false, ValidationAllowedAlgorithms: []string{"sha256"}, ValidationTestMode: false}
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, tempDir, cfg)

	ctx := context.Background()
	user := createTestUser(t, userRepo, ctx, "videouser4", "video4@example.com")

	// ~128 KiB file
	samplePath := filepath.Join(t.TempDir(), "mismatch.mp4")
	if err := writeMinimalMP4(samplePath, 128*1024); err != nil {
		t.Fatalf("create sample: %v", err)
	}
	fileBytes, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}

	chunkSize := int64(16 * 1024)
	// Initiate
	req := domain.InitiateUploadRequest{FileName: "mismatch.mp4", FileSize: int64(len(fileBytes)), ChunkSize: chunkSize}
	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/api/v1/uploads/initiate", bytes.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), middleware.UserIDKey, user.ID))
	w := httptest.NewRecorder()
	InitiateUploadHandler(uploadService, videoRepo)(w, httpReq)
	if w.Code != http.StatusCreated {
		t.Fatalf("initiate failed: %d %s", w.Code, w.Body.String())
	}
	var env Response
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	var initResp domain.InitiateUploadResponse
	dataBytes, _ := json.Marshal(env.Data)
	_ = json.Unmarshal(dataBytes, &initResp)

	// Upload first two chunks correctly
	totalChunks := (len(fileBytes) + int(chunkSize) - 1) / int(chunkSize)
	for i := 0; i < totalChunks && i < 2; i++ {
		start := i * int(chunkSize)
		end := start + int(chunkSize)
		if end > len(fileBytes) {
			end = len(fileBytes)
		}
		chunk := fileBytes[start:end]
		h := sha256.New()
		h.Write(chunk)
		checksum := hex.EncodeToString(h.Sum(nil))
		httpReq = httptest.NewRequest("POST", "/api/v1/uploads/"+initResp.SessionID+"/chunks", bytes.NewReader(chunk))
		httpReq.Header.Set("X-Chunk-Index", strconv.Itoa(i))
		httpReq.Header.Set("X-Chunk-Checksum", checksum)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("sessionId", initResp.SessionID)
		httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))
		w = httptest.NewRecorder()
		UploadChunkHandler(uploadService, cfg)(w, httpReq)
		if w.Code != http.StatusOK {
			t.Fatalf("upload i=%d failed: %d %s", i, w.Code, w.Body.String())
		}
	}

	// Now try to upload chunk 2 with bad checksum to trigger 400
	badIndex := 2
	if badIndex < totalChunks {
		start := badIndex * int(chunkSize)
		end := start + int(chunkSize)
		if end > len(fileBytes) {
			end = len(fileBytes)
		}
		chunk := fileBytes[start:end]
		httpReq = httptest.NewRequest("POST", "/api/v1/uploads/"+initResp.SessionID+"/chunks", bytes.NewReader(chunk))
		httpReq.Header.Set("X-Chunk-Index", strconv.Itoa(badIndex))
		httpReq.Header.Set("X-Chunk-Checksum", "deadbeef") // invalid
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("sessionId", initResp.SessionID)
		httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))
		w = httptest.NewRecorder()
		UploadChunkHandler(uploadService, cfg)(w, httpReq)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 on mismatch, got %d", w.Code)
		}
		var envErr Response
		_ = json.Unmarshal(w.Body.Bytes(), &envErr)
		if envErr.Success || envErr.Error == nil || envErr.Error.Code == "" {
			t.Fatalf("expected error envelope on mismatch")
		}
	}

	// Resume to check state (should list uploaded chunks [0,1] and include 2 as remaining)
	httpReq = httptest.NewRequest("GET", "/api/v1/uploads/"+initResp.SessionID+"/resume", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sessionId", initResp.SessionID)
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))
	w = httptest.NewRecorder()
	ResumeUploadHandler(uploadService)(w, httpReq)
	if w.Code != http.StatusOK {
		t.Fatalf("resume failed: %d %s", w.Code, w.Body.String())
	}
	var env2 Response
	_ = json.Unmarshal(w.Body.Bytes(), &env2)
	var resumeMap map[string]interface{}
	b2, _ := json.Marshal(env2.Data)
	_ = json.Unmarshal(b2, &resumeMap)
	// uploaded_chunks should contain indices 0 and 1
	ulist := resumeMap["uploaded_chunks"].([]interface{})
	if len(ulist) < 2 {
		t.Fatalf("expected at least 2 uploaded chunks, got %d", len(ulist))
	}

	// Upload the bad chunk with correct checksum this time
	if badIndex < totalChunks {
		start := badIndex * int(chunkSize)
		end := start + int(chunkSize)
		if end > len(fileBytes) {
			end = len(fileBytes)
		}
		chunk := fileBytes[start:end]
		h := sha256.New()
		h.Write(chunk)
		checksum := hex.EncodeToString(h.Sum(nil))
		httpReq = httptest.NewRequest("POST", "/api/v1/uploads/"+initResp.SessionID+"/chunks", bytes.NewReader(chunk))
		httpReq.Header.Set("X-Chunk-Index", strconv.Itoa(badIndex))
		httpReq.Header.Set("X-Chunk-Checksum", checksum)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("sessionId", initResp.SessionID)
		httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))
		w = httptest.NewRecorder()
		UploadChunkHandler(uploadService, cfg)(w, httpReq)
		if w.Code != http.StatusOK {
			t.Fatalf("re-upload mismatch chunk failed: %d %s", w.Code, w.Body.String())
		}
	}

	// Upload all remaining chunks to complete the file
	for i := badIndex + 1; i < totalChunks; i++ {
		start := i * int(chunkSize)
		end := start + int(chunkSize)
		if end > len(fileBytes) {
			end = len(fileBytes)
		}
		chunk := fileBytes[start:end]
		h := sha256.New()
		h.Write(chunk)
		checksum := hex.EncodeToString(h.Sum(nil))
		httpReq = httptest.NewRequest("POST", "/api/v1/uploads/"+initResp.SessionID+"/chunks", bytes.NewReader(chunk))
		httpReq.Header.Set("X-Chunk-Index", strconv.Itoa(i))
		httpReq.Header.Set("X-Chunk-Checksum", checksum)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("sessionId", initResp.SessionID)
		httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))
		w = httptest.NewRecorder()
		UploadChunkHandler(uploadService, cfg)(w, httpReq)
		if w.Code != http.StatusOK {
			t.Fatalf("upload remaining i=%d failed: %d %s", i, w.Code, w.Body.String())
		}
	}

	// Complete and verify output equals original
	httpReq = httptest.NewRequest("POST", "/api/v1/uploads/"+initResp.SessionID+"/complete", nil)
	rctx = chi.NewRouteContext()
	rctx.URLParams.Add("sessionId", initResp.SessionID)
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))
	w = httptest.NewRecorder()
	CompleteUploadHandler(uploadService, encodingRepo, nil)(w, httpReq)
	if w.Code != http.StatusOK {
		t.Fatalf("complete failed: %d %s", w.Code, w.Body.String())
	}

	session, err := uploadRepo.GetSession(ctx, initResp.SessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	sp := storage.NewPaths(tempDir)
	assembledPath := sp.WebVideoFilePath(session.VideoID, filepath.Ext(req.FileName))
	out, err := os.ReadFile(assembledPath)
	if err != nil {
		t.Fatalf("read assembled: %v", err)
	}
	if !bytes.Equal(fileBytes, out) {
		t.Fatalf("assembled mismatch: got %d want %d", len(out), len(fileBytes))
	}
}

func TestStrictModeRequiresChecksum(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}
	testDB := testutil.SetupTestDB(t)
	if testDB == nil { // skipped
		return
	}
	uploadRepo := repository.NewUploadRepository(testDB.DB)
	encodingRepo := repository.NewEncodingRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)

	tempDir := t.TempDir()
	// Strict mode on, test mode off
	strictCfg := &config.Config{ValidationStrictMode: true, ValidationAllowedAlgorithms: []string{"sha256"}, ValidationTestMode: false}
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, tempDir, strictCfg)

	ctx := context.Background()
	user := createTestUser(t, userRepo, ctx, "videouser5", "video5@example.com")

	// Small file
	samplePath := filepath.Join(t.TempDir(), "strict.mp4")
	if err := writeMinimalMP4(samplePath, 64*1024); err != nil {
		t.Fatalf("create sample: %v", err)
	}
	fileBytes, _ := os.ReadFile(samplePath)

	// Initiate
	req := domain.InitiateUploadRequest{FileName: "strict.mp4", FileSize: int64(len(fileBytes)), ChunkSize: 8 * 1024}
	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/api/v1/uploads/initiate", bytes.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), middleware.UserIDKey, user.ID))
	w := httptest.NewRecorder()
	InitiateUploadHandler(uploadService, videoRepo)(w, httpReq)
	if w.Code != http.StatusCreated {
		t.Fatalf("initiate failed: %d %s", w.Code, w.Body.String())
	}
	var env Response
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	var initResp domain.InitiateUploadResponse
	db, _ := json.Marshal(env.Data)
	_ = json.Unmarshal(db, &initResp)

	// Try to upload first chunk WITHOUT checksum header
	chunk := fileBytes[:8*1024]
	httpReq = httptest.NewRequest("POST", "/api/v1/uploads/"+initResp.SessionID+"/chunks", bytes.NewReader(chunk))
	httpReq.Header.Set("X-Chunk-Index", "0")
	// No X-Chunk-Checksum header on purpose
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sessionId", initResp.SessionID)
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))
	w = httptest.NewRecorder()
	UploadChunkHandler(uploadService, strictCfg)(w, httpReq)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 in strict mode missing checksum, got %d", w.Code)
	}

	var envErr Response
	_ = json.Unmarshal(w.Body.Bytes(), &envErr)
	if envErr.Success || envErr.Error == nil || envErr.Error.Code != "MISSING_CHECKSUM" {
		t.Fatalf("expected MISSING_CHECKSUM error, got: %+v", envErr)
	}
}

// no extra helpers
