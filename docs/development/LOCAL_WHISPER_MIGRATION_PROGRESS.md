# Local Whisper Migration Progress

## Objective
Migrate from OpenAI Whisper API to a local Whisper service with comprehensive multi-language caption support.

## Completed Work

### 1. Docker Service Setup ✅
- Added Whisper service to `docker-compose.yml`
- Using `onerahmet/openai-whisper-asr-webservice:latest-gpu` image
- Configured with base model, 4G memory limit
- Includes health checks and volume mounting for model caching
- Service port: 9000

### 2. HTTP Whisper Client Implementation ✅
- Created `internal/whisper/http_client.go`
- Implements the Whisper Client interface
- Communicates with HTTP Whisper service via REST API
- Supports audio transcription with language detection
- Handles VTT and SRT caption formatting

### 3. Configuration Updates ✅
- Added `WHISPER_API_URL` configuration parameter
- Updated `internal/config/config.go` with new field
- Updated `internal/whisper/client.go` to support HTTP-based provider
- Modified validation logic to handle HTTP API URL

### 4. Multi-Language Caption Support ✅
- Improved caption regeneration logic in `internal/usecase/captiongen/service.go`
- **Language-Specific Deletion**: When regenerating a caption for a specific language (e.g., English), only the caption for that language is deleted
- **Multi-Language Preservation**: Other language captions (e.g., Spanish, French) remain untouched during regeneration
- **Auto-Detect Handling**: When auto-detecting language, deletion occurs after transcription to avoid removing wrong captions
- Comprehensive documentation added to explain the behavior

### 5. Comprehensive Tests ✅
- Created `internal/usecase/captiongen/service_test.go`
- Test Coverage:
  - `TestRegenerateCaptionWithSpecificLanguage`: Verifies only specified language is deleted
  - `TestRegenerateCaptionMultiLanguagePreservation`: Ensures other languages remain intact
  - `TestRegenerateCaptionAutoDetect`: Validates auto-detect doesn't delete prematurely
  - `TestCreateJob`: Tests job creation workflow
  - `TestCreateJobVideoNotProcessed`: Tests error handling for unprocessed videos
  - `TestGetJobStatus`: Tests job status retrieval
  - `TestGetJobsByVideo`: Tests listing all jobs for a video
  - `TestGetLanguageLabel`: Tests language code to label mapping

### 6. Bug Fixes ✅
- Fixed type conversion in `internal/whisper/local_client.go` (int to float64 conversion for timestamps)

## Remaining Work

### 1. Type Compatibility Issues 🔴 CRITICAL
The caption generation service has type mismatches with the domain model:

**Issues to Fix:**
- `Video.ID` is `string`, not `uuid.UUID` - need to convert appropriately
- `Video.Status` is the field name, not `Video.ProcessingStatus`
- `Video.Language` is `string`, not `*string` (pointer)
- `storage.OriginalVideoPath()` doesn't exist as a package function
  - Need to use `storage.NewPaths(uploadsDir)` and call methods on the Paths struct
  - Example: `sp := storage.NewPaths(s.uploadsDir); path := sp.WebVideoFilePath(video.ID, ext)`

**Files Needing Updates:**
- `internal/usecase/captiongen/service.go` (lines 159, 164-169, 174-175, 265, 274, 311, 356-362)

### 2. Integration Testing 🟡
- Wire up caption generation service in app initialization
- Test end-to-end caption generation with Docker Whisper service
- Verify multi-language caption scenarios work in practice

### 3. CI/CD Pipeline 🟡
- Ensure Whisper service is available in CI environment (or mock it)
- Add Whisper service to `docker-compose.test.yml` if needed
- Update test configuration to handle Whisper dependency

### 4. Documentation 🟢
- Update README with local Whisper setup instructions
- Document environment variables for Whisper configuration
- Add troubleshooting guide for Whisper service issues

## Environment Variables

```bash
# Enable caption generation
ENABLE_CAPTION_GENERATION=true

# Provider: 'local' for HTTP service or whisper.cpp
WHISPER_PROVIDER=local

# HTTP Whisper Service URL (for Docker setup)
WHISPER_API_URL=http://whisper:9000

# Model size: tiny, base, small, medium, large
WHISPER_MODEL_SIZE=base

# Number of concurrent caption generation workers
CAPTION_GENERATION_WORKERS=2

# Legacy whisper.cpp settings (not used with HTTP service)
# WHISPER_CPP_PATH=/usr/local/bin/whisper
# WHISPER_MODELS_DIR=/var/lib/whisper/models

# OpenAI API (alternative provider)
# WHISPER_OPENAI_API_KEY=sk-...
```

## Docker Compose Configuration

The Whisper service is now included in `docker-compose.yml`:

```yaml
whisper:
  image: onerahmet/openai-whisper-asr-webservice:latest-gpu
  restart: unless-stopped
  environment:
    - ASR_MODEL=base
    - ASR_ENGINE=openai_whisper
  ports:
    - "9000:9000"
  volumes:
    - whisper_models:/root/.cache/whisper
  healthcheck:
    test: ["CMD", "curl", "-f", "http://localhost:9000/"]
    interval: 30s
    timeout: 10s
    retries: 3
    start_period: 60s
```

## Testing Multi-Language Caption Support

### Scenario 1: Generate English Caption
```bash
POST /api/v1/videos/{id}/captions/generate
{
  "target_language": "en",
  "model_size": "base",
  "output_format": "vtt"
}
```
**Expected**: English caption is generated.

### Scenario 2: Generate Spanish Caption (English exists)
```bash
POST /api/v1/videos/{id}/captions/generate
{
  "target_language": "es",
  "model_size": "base",
  "output_format": "vtt"
}
```
**Expected**: Spanish caption is generated. English caption remains untouched.

### Scenario 3: Regenerate English Caption (English and Spanish exist)
```bash
POST /api/v1/videos/{id}/captions/generate
{
  "target_language": "en",
  "model_size": "base",
  "output_format": "vtt"
}
```
**Expected**: English caption is replaced with new version. Spanish caption remains untouched.

### Scenario 4: Auto-Detect Language (Multiple captions exist)
```bash
POST /api/v1/videos/{id}/captions/generate
{
  "model_size": "base",
  "output_format": "vtt"
}
```
**Expected**:
- Audio is transcribed, language is auto-detected (e.g., "en")
- Only the caption for the detected language ("en") is replaced
- Other language captions ("es", "fr", etc.) remain untouched

## Next Steps

1. **Fix Type Compatibility Issues** (HIGH PRIORITY)
   - Update caption generation service to use correct types
   - Use `storage.Paths` struct methods instead of package functions
   - Test compilation

2. **Run Full Test Suite**
   - Execute unit tests: `go test ./internal/usecase/captiongen/...`
   - Execute integration tests: `make test-integration`
   - Fix any failing tests

3. **Test with Docker Stack**
   - Start full stack: `docker compose up`
   - Test caption generation API endpoints
   - Verify multi-language scenarios

4. **CI/CD Updates**
   - Ensure tests pass in CI environment
   - Update GitHub Actions workflow if needed

## Commands

```bash
# Build and start services
docker compose up --build

# Run caption generation tests
go test ./internal/usecase/captiongen/... -v

# Run all tests
make test

# Check lint
make lint

# Build application
go build -o bin/athena ./cmd/server
```

## Notes

- The HTTP Whisper client is designed to be drop-in compatible with existing code
- Multi-language support ensures captions can coexist without conflicts
- The Docker Whisper service uses GPU acceleration if available
- Model downloads are cached in a Docker volume for faster startup

## References

- Whisper Docker Image: https://github.com/ahmetoner/whisper-asr-webservice
- OpenAI Whisper: https://github.com/openai/whisper
- Whisper.cpp: https://github.com/ggerganov/whisper.cpp
