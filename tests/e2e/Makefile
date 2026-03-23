# E2E Testing Makefile

.PHONY: help start stop restart logs test test-verbose clean fixtures setup

# Default target
help:
	@echo "E2E Testing Makefile Commands:"
	@echo "  make setup         - Complete E2E test environment setup (fixtures + start)"
	@echo "  make start         - Start test environment (Docker Compose)"
	@echo "  make stop          - Stop test environment"
	@echo "  make restart       - Restart test environment"
	@echo "  make logs          - View logs from all services"
	@echo "  make test          - Run all E2E tests"
	@echo "  make test-verbose  - Run E2E tests with verbose output"
	@echo "  make fixtures      - Generate test fixtures (video files)"
	@echo "  make clean         - Stop environment and remove volumes"
	@echo "  make db-shell      - Access PostgreSQL shell"
	@echo "  make redis-shell   - Access Redis CLI"

# Complete setup (fixtures + start services)
setup: fixtures start
	@echo "Waiting for services to be ready..."
	@timeout 120 bash -c 'until curl -sf http://localhost:18080/health > /dev/null; do echo "Waiting for API..."; sleep 5; done'
	@echo "✓ E2E test environment is ready!"

# Start test environment
start:
	@echo "Starting E2E test environment..."
	docker compose -f docker compose.yml up -d
	@echo "✓ Services started"

# Stop test environment
stop:
	@echo "Stopping E2E test environment..."
	docker compose -f docker compose.yml stop
	@echo "✓ Services stopped"

# Restart test environment
restart:
	@echo "Restarting E2E test environment..."
	docker compose -f docker compose.yml restart
	@echo "✓ Services restarted"

# View logs
logs:
	docker compose -f docker compose.yml logs -f

# Run E2E tests
test:
	@echo "Running E2E tests..."
	E2E_BASE_URL=http://localhost:18080 go test -v -timeout 30m ./scenarios/...

# Run E2E tests with verbose output
test-verbose:
	@echo "Running E2E tests (verbose)..."
	E2E_BASE_URL=http://localhost:18080 go test -v -count=1 -timeout 30m ./scenarios/... 2>&1 | tee e2e_test_output.log

# Generate test fixtures
fixtures:
	@echo "Generating test fixtures..."
	@mkdir -p fixtures
	@if command -v ffmpeg > /dev/null; then \
		echo "Generating 480p test video (5 seconds)..."; \
		ffmpeg -y -f lavfi -i testsrc=duration=5:size=640x480:rate=24 \
			-vf "drawtext=text='Test Video 480p':fontsize=32:fontcolor=white:x=(w-text_w)/2:y=(h-text_h)/2" \
			-c:v libx264 -preset ultrafast -pix_fmt yuv420p \
			fixtures/test_video_480p.mp4 2>/dev/null; \
		echo "Generating 720p test video (10 seconds)..."; \
		ffmpeg -y -f lavfi -i testsrc=duration=10:size=1280x720:rate=30 \
			-vf "drawtext=text='Test Video 720p':fontsize=48:fontcolor=white:x=(w-text_w)/2:y=(h-text_h)/2" \
			-c:v libx264 -preset fast -pix_fmt yuv420p \
			fixtures/test_video_720p.mp4 2>/dev/null; \
		echo "Generating test video with audio..."; \
		ffmpeg -y -f lavfi -i testsrc=duration=5:size=1280x720:rate=30 \
			-f lavfi -i sine=frequency=1000:duration=5 \
			-c:v libx264 -c:a aac -b:a 128k \
			fixtures/test_video_with_audio.mp4 2>/dev/null; \
		echo "✓ Test videos generated"; \
	else \
		echo "ERROR: ffmpeg not found. Please install ffmpeg to generate test fixtures."; \
		exit 1; \
	fi
	@echo "Creating EICAR test file (safe malware test)..."
	@echo 'X5O!P%@AP[4\PZX54(P^)7CC)7}$$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$$H+H*' > fixtures/eicar.com
	@echo "✓ Fixtures generated successfully"

# Clean up environment and volumes
clean:
	@echo "Cleaning up E2E test environment..."
	docker compose -f docker compose.yml down -v
	@echo "✓ Environment cleaned"

# Access PostgreSQL shell
db-shell:
	docker exec -it vidra-e2e-postgres psql -U vidra_test -d vidra_e2e

# Access Redis CLI
redis-shell:
	docker exec -it vidra-e2e-redis redis-cli

# Check service status
status:
	@echo "Service Status:"
	@docker compose -f docker compose.yml ps

# Run specific test
test-single:
	@echo "Running specific test: $(TEST)"
	E2E_BASE_URL=http://localhost:18080 go test -v -timeout 30m ./scenarios/ -run $(TEST)
