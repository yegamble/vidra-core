# Athena - PeerTube Backend in Go

[![Test](https://github.com/yegamble/athena/actions/workflows/test.yml/badge.svg)](https://github.com/yegamble/athena/actions/workflows/test.yml)
[![OpenAPI CI](https://github.com/yegamble/athena/actions/workflows/openapi-ci.yml/badge.svg)](https://github.com/yegamble/athena/actions/workflows/openapi-ci.yml)

A high-performance PeerTube backend implementation in Go with decentralized storage support. This project implements core PeerTube API compatibility with additional features for a modern video platform.

## Features

### Core PeerTube Compatibility (Completed Sprints A-G) ✅
- 📺 **Channels** - Full channel system where videos belong to channels, not users directly
- 🔄 **Channel Subscriptions** - Subscribe to channels with backward compatibility for user subscriptions
- 💭 **Threaded Comments** - Nested comments with moderation, flagging, and auto-hide functionality
- 👍 **Ratings & Playlists** - Like/dislike videos, create playlists with privacy controls
- 📝 **Captions/Subtitles** - VTT/SRT subtitle support with multi-language capabilities
- 🔑 **OAuth2 Complete** - Authorization Code flow with PKCE, scopes, token introspection
- 🛡️ **Admin & Moderation** - Abuse reports, blocklist management, instance configuration
- 🔗 **oEmbed Support** - Standard video embedding protocol (JSON/XML)

### Video Platform Features
- 🚀 **High Performance** - Built with Go for maximum concurrency and speed
- 📝 **OpenAPI 3.0** - Complete API specification with automatic validation
- 🔐 **JWT Authentication** - HS256 access tokens with refresh rotation
- 🛡️ **Production Security** - Comprehensive security headers, rate limiting, CORS
- 🗄️ **PostgreSQL** - Robust database with full-text search capabilities
- ⚡ **Redis** - Fast caching and session management
- 🌐 **IPFS** - Decentralized storage support
- 🎥 **Video Processing** - FFmpeg integration for transcoding
- 📁 **Video Categories** - Comprehensive categorization system with 15 default categories
- 🔔 **Real-time Notifications** - Automatic notifications for video uploads, messages, comments, and user interactions
- 💬 **Messaging System** - Direct messaging between users with E2EE support
- 🖼️ **Avatar WebP Optimization** - Optional WebP encoding for uploaded avatars (quality configurable), IPFS pinning of both original and WebP variants
- 🌐 **ATProto Federation** - Cross-platform content federation via AT Protocol with Bluesky integration
- 📊 **Observability** - Prometheus metrics, structured logging, distributed tracing
- 🐳 **Docker Ready** - Full containerization with Docker Compose
- ✅ **CI/CD** - GitHub Actions with automated testing
- 🔄 **Zero-Downtime Deployments** - Health checks and graceful shutdown

## Project Status

### ✅ Completed PeerTube Sprints (A-G)
- **Sprint A: Channels** - Complete channel system implementation
- **Sprint B: Channel Subscriptions** - Channel-based subscriptions with backward compatibility
- **Sprint C: Comments** - Threaded comments with moderation
- **Sprint D: Ratings & Playlists** - Video ratings and playlist management
- **Sprint E: Captions** - Multi-language subtitle support
- **Sprint F: OAuth2** - Full OAuth2 implementation with Authorization Code + PKCE
- **Sprint G: Admin & Instance** - Admin tools, instance config, oEmbed

### 🚧 In Progress (Federation Sprints H-K)
- **Sprint H: Federation Foundations** ✅ - ATProto integration complete
  - Instance DID document with handle verification
  - XRPC endpoints for federation communication
  - Bluesky firehose subscription and timeline aggregation
  - Post creation when videos are published
- **Sprint I: Federation Videos** 🚧 - Video federation via ATProto
- **Sprint J: Federation Social** - Follows, likes, comments over ATProto
- **Sprint K: Federation Hardening** - Reliability and moderation for ATProto

### 🔗 ATProto Federation Features (Active)
- **Instance Identity**: Serves DID document at `/.well-known/atproto-did`
- **Bluesky Integration**:
  - Automatic post creation when public videos are published
  - Subscribe to Bluesky firehose for real-time updates
  - Federated timeline aggregation from configured actors
- **Admin Controls**:
  - Enable/disable federation per instance
  - Manage federation peers and subscriptions
  - Monitor federation health and metrics
- **API Endpoints**:
  - `GET /api/v1/federation/timeline` - Federated content timeline
  - `GET /api/v1/federation/status` - Federation health status
  - Admin endpoints for federation management

See [docs/sprints.md](docs/sprints.md) for detailed sprint information.

## Quick Start

### Prerequisites

- Go 1.23.4+
- Docker & Docker Compose
- PostgreSQL 15+
- Redis 7+
- Node.js 18+ (for API documentation tools)

### One-Command Setup

```bash
# Clone the repository
git clone https://github.com/yegamble/athena.git
cd athena

# Run complete setup
make setup
```

This will:
1. Copy `.env.example` to `.env`
2. Install dependencies
3. Install development tools
4. Start Docker services
5. Run database migrations
6. Set up the development environment

### Manual Setup

#### 1. Clone and Configure

```bash
# Clone the repository
git clone https://github.com/yegamble/athena.git
cd athena

# Copy environment variables
cp .env.example .env
# Edit .env with your configuration
```

#### 2. Start Services with Docker

```bash
# Start all services (PostgreSQL, Redis, App)
make docker-up

# Or using docker-compose directly
docker-compose up -d
```

#### 3. Run Development Server

```bash
# Install dependencies
make deps

# Run development server with hot reload
make dev

# Or without hot reload
go run ./cmd/server
```

The API will be available at `http://localhost:8080`

## Development

### Available Make Commands

```bash
make help          # Show all available commands
make deps          # Download dependencies
make lint          # Run linting
make test          # Run tests with coverage
make test-local    # Run tests with local Docker services
make build         # Build binary
make docker        # Build Docker image
make docker-up     # Start Docker services
make docker-down   # Stop Docker services
make docker-logs   # View Docker logs
make docker-reset  # Reset Docker environment
make dev           # Run development server
make clean         # Clean build artifacts
```

### Running Tests

```bash
# Run all tests
make test

# Run tests with local Docker services
make test-local

# Run tests in CI environment
make test-ci

# View coverage report
open coverage.html
```

#### Offline/toolchain note

This repo uses the Go toolchain directive in `go.mod` (>= 1.23.4). By default, newer Go versions may auto-download the requested toolchain. In locked-down environments where network is blocked, you have two options:

- Install Go 1.23.4 (or newer) locally and run with the offline toggle: `GO_OFFLINE=1 make test-integration-ci` (equivalent to `GOTOOLCHAIN=local`).
- Or export `GOTOOLCHAIN=local` directly: `GOTOOLCHAIN=local make test-integration-ci`.

Note: The offline toggle disables auto-download. Your locally installed Go must still satisfy the version in `go.mod`, otherwise the build will fail.

### Integration Tests

Integration tests use a real Postgres (and ping Redis) via the helpers in `internal/testutil`. You can configure the test database through environment variables. The loader checks in this order:

- `TEST_DATABASE_URL`: Full Postgres URL used only for tests
- `DATABASE_URL`: Fallback if `TEST_DATABASE_URL` is not set
- Granular fallbacks (if neither URL is set):
  - `TEST_DB_HOST` (default: `localhost`)
  - `TEST_DB_PORT` (default: `5433`)
  - `TEST_DB_NAME` (default: `athena_test`)
  - `TEST_DB_USER` (default: `test_user`)
  - `TEST_DB_PASSWORD` (default: `test_password`)
  - `TEST_DB_SSLMODE` (default: `disable`)

Additionally, the test bootstrap attempts to load `.env.test` first, then `.env` if present, so you can commit a dedicated test configuration.

Examples:

```bash
# Use a single URL for test DB
export TEST_DATABASE_URL=postgres://user:pass@localhost:5432/athena_test?sslmode=disable

# Or use granular overrides
export TEST_DB_HOST=localhost
export TEST_DB_PORT=5432
export TEST_DB_NAME=athena_test
export TEST_DB_USER=postgres
export TEST_DB_PASSWORD=postgres

# If your Redis differs from the default test instance
export REDIS_URL=redis://localhost:6379/0

# Run only integration tests in httpapi package
go test ./internal/httpapi -run Integration

# Or run all tests
go test ./...
```

### API Documentation

The API is defined using OpenAPI 3.0 specification in `api/openapi.yaml`.

**Documentation Resources:**
- 📄 **OpenAPI Specifications**:
  - Main API: [api/openapi.yaml](api/openapi.yaml)
  - Channels & Subscriptions: [api/openapi_channels.yaml](api/openapi_channels.yaml)
  - Comments: [api/openapi_comments.yaml](api/openapi_comments.yaml)
  - Ratings & Playlists: [api/openapi_ratings_playlists.yaml](api/openapi_ratings_playlists.yaml)
  - Moderation & Admin: [api/openapi_moderation.yaml](api/openapi_moderation.yaml)
  - Federation & ATProto: [api/openapi_federation.yaml](api/openapi_federation.yaml)
  - Notifications: [docs/openapi_notifications.yaml](docs/openapi_notifications.yaml)
- 📚 **Implementation Guides**:
  - API Examples: [docs/API_EXAMPLES.md](docs/API_EXAMPLES.md)
  - OAuth2 Guide: [docs/OAUTH2.md](docs/OAUTH2.md)
  - Notifications: [docs/NOTIFICATIONS_API.md](docs/NOTIFICATIONS_API.md)
  - Sprint Planning: [docs/sprints.md](docs/sprints.md)
- 🔔 **Feature Documentation**:
  - Channels system with PeerTube compatibility
  - Threaded comments with moderation
  - Video ratings and playlists
  - Multi-language captions support
  - Complete OAuth2 implementation
  - Instance configuration management

```bash
# Install documentation tools (one-time setup)
npm install -g @redocly/cli@latest
npm install -g @apidevtools/swagger-cli@latest

# Validate OpenAPI spec
make validate-openapi

# Generate HTML documentation
make generate-docs

# Serve API documentation locally
make serve-docs
# Opens at http://localhost:8081
```

**Note**: The documentation generation uses modern tools that require Node.js 20+. The old `spectacle-docs` package is deprecated and should not be used.

## API Endpoints

### Authentication

- `POST /auth/register` - Register new user
- `POST /auth/login` - Login with email/password
- `POST /auth/refresh` - Refresh access token
- `POST /auth/logout` - Logout (requires auth)

### OAuth2 (Full Implementation)

**Authorization Endpoints:**
- `GET /oauth/authorize` - Authorization endpoint (Authorization Code flow with PKCE)
- `POST /oauth/token` - Token endpoint (supports authorization_code, password, refresh_token, client_credentials grants)
- `POST /oauth/revoke` - Revoke access or refresh tokens
- `POST /oauth/introspect` - Token introspection endpoint

**Client Management (Admin Only):**
- `GET /api/v1/admin/oauth/clients` - List OAuth clients
- `POST /api/v1/admin/oauth/clients` - Create new OAuth client
- `PUT /api/v1/admin/oauth/clients/{id}` - Update OAuth client
- `DELETE /api/v1/admin/oauth/clients/{id}` - Delete OAuth client

**Supported OAuth2 Features:**
- Authorization Code flow with PKCE (recommended)
- Password grant (for first-party apps)
- Client credentials grant
- Refresh token rotation
- Token revocation (RFC 7009)
- Token introspection (RFC 7662)
- Comprehensive scope system
- WWW-Authenticate headers on 401 responses

See [docs/OAUTH2.md](docs/OAUTH2.md) for detailed OAuth2 documentation.

### Health Checks

- `GET /health` - Basic health check
- `GET /ready` - Readiness check (DB, Redis, IPFS)

### Videos

**Public Endpoints:**
 - `GET /api/v1/videos` - List public videos (supports pagination, filtering, sorting)
 - `GET /api/v1/videos/search` - Search videos with full-text search and filters
- `GET /api/v1/videos/{id}` - Get video details
- `GET /api/v1/videos/{id}/stream` - Stream video (HLS playlist, `quality` query param supports 240p-4320p, default 720p)
- `GET /api/v1/videos/qualities` - List supported quality labels and the default
  - Response body (wrapped):
    - `data.qualities`: array of strings (e.g., `["240p","360p","480p","720p","1080p","1440p","2160p","4320p"]`)
    - `data.default`: default quality string (e.g., `"720p"`)
  - Notes:
    - The default is also used when `quality` is omitted in `/stream`.
    - The set returned here reflects server-side support and validation.
- `GET /api/v1/videos/top` - Get top/most viewed videos within a time period
- `GET /api/v1/trending` - Get currently trending videos
- `GET /api/v1/hls/*` - Serve HLS playlists and segments
- `POST /api/v1/views/fingerprint` - Generate fingerprint for view deduplication

#### Resolution Detection Logic (Encoding)
When queuing an encoding job after upload completes, the service determines the source resolution using the following rules:

- Prefer exact height from metadata when available: `source = DetectResolutionFromHeight(height)`.
- If height is missing but width is available, estimate height using aspect ratio:
  - Accepts aspect ratio formats: `16:9`, `9/16`, and numeric (e.g., `1.7778`).
  - Defaults to `16:9` if aspect ratio is missing or invalid.
  - Estimated height: `round(width / aspectRatio)` then `source = DetectResolutionFromHeight(estimatedHeight)`.
- Out-of-range heights clamp to nearest supported (<= 240 → 240p, >= 4320 → 4320p).
- Ties are resolved by preferring the lower resolution (e.g., exactly between 720p and 1080p picks 720p).

Examples:
- `{ height: 900 }` → closest to 720p vs 1080p; tie prefers lower → `720p`.
- `{ width: 1280, aspect_ratio: "16:9" }` → estimated height `≈ 720` → `720p`.
- `{ width: 1920, aspect_ratio: "16:9" }` → estimated height `≈ 1080` → `1080p`.
- `{ width: 1024, aspect_ratio: "4:3" }` → estimated height `≈ 768` → `720p` (closer to 720p than 1080p).
- `{ width: 1920 }` (no AR) → defaults to `16:9` → `1080p`.

Operational note: Debug logs for width/aspect estimation emit only when `LOG_LEVEL` is `debug` or `trace`.

**Protected Endpoints (Require Authentication):**
- `POST /api/v1/videos` - Create video metadata
- `PUT /api/v1/videos/{id}` - Update video (owner only)
- `DELETE /api/v1/videos/{id}` - Delete video (owner only)
- `POST /api/v1/videos/upload` - Legacy one-shot video upload (for Postman compatibility)
- `POST /api/v1/videos/{id}/upload` - Upload video chunk
- `POST /api/v1/videos/{id}/complete` - Complete chunked upload
- `GET /api/v1/videos/subscriptions` - Get videos from subscribed channels
- `POST /api/v1/videos/{videoId}/views` - Track video view
- `GET /api/v1/videos/{videoId}/analytics` - Get video analytics (owner only)
- `GET /api/v1/videos/{videoId}/stats/daily` - Get daily video statistics (owner only)

### Video Categories

**Public Endpoints:**
- `GET /api/v1/categories` - List all active categories
  - Query Parameters:
    - `active_only` (boolean): Filter to active categories only (default: true)
    - `order_by` (string): Sort field - `name`, `slug`, `display_order`, `created_at` (default: `display_order`)
    - `order_dir` (string): Sort direction - `asc`, `desc` (default: `asc`)
    - `limit` (integer): Max results per page (1-100, default: 50)
    - `offset` (integer): Pagination offset (default: 0)
- `GET /api/v1/categories/{id}` - Get category by ID or slug
  - Accepts either UUID or slug identifier

**Admin Endpoints (Require Admin Role):**
- `POST /api/v1/admin/categories` - Create new category
  - Request Body:
    ```json
    {
      "name": "Music",
      "slug": "music",
      "description": "Music videos and audio content",
      "icon": "🎵",
      "color": "#FF0000",
      "display_order": 1,
      "is_active": true
    }
    ```
- `PUT /api/v1/admin/categories/{id}` - Update category
  - All fields are optional in update request
  - Cannot change slug of the default "other" category
- `DELETE /api/v1/admin/categories/{id}` - Delete category
  - Cannot delete the default "other" category
  - Videos with deleted category will have category_id set to NULL

**Default Categories:**
The system comes with 15 pre-defined categories:
- Music, Gaming, Education, Entertainment, News & Politics
- Science & Technology, Sports, Travel & Events, Film & Animation
- People & Blogs, Pets & Animals, How-to & Style, Autos & Vehicles
- Nonprofits & Activism, Other (default fallback)

**Video Category Assignment:**
- Videos can have one category assigned via `category_id` field
- Category is optional; videos without a category will use NULL
- When creating/updating videos, include `category_id` in the request:
  ```json
  {
    "title": "My Video",
    "category_id": "a7808f7e-6762-4c9a-a42a-923d8a7fc770"
  }
  ```

### Uploads

**Chunked Upload Endpoints (Require Authentication):**
- `POST /api/v1/uploads/initiate` - Initiate a chunked upload session
- `POST /api/v1/uploads/{sessionId}/chunks` - Upload a chunk
- `POST /api/v1/uploads/{sessionId}/complete` - Complete upload and trigger processing
- `GET /api/v1/uploads/{sessionId}/status` - Get upload session status
- `GET /api/v1/uploads/{sessionId}/resume` - Get information to resume an interrupted upload

### Encoding

**Public Endpoints:**
- `GET /api/v1/encoding/status` - Get encoding job status (optionally filter by videoId)

### Channels

**Public Endpoints:**
- `GET /api/v1/channels` - List all channels (with pagination)
- `GET /api/v1/channels/{id}` - Get channel by ID or handle
- `GET /api/v1/channels/{id}/videos` - Get videos from a channel

**Protected Endpoints (Require Authentication):**
- `POST /api/v1/channels` - Create a new channel
- `PUT /api/v1/channels/{id}` - Update channel (owner only)
- `DELETE /api/v1/channels/{id}` - Delete channel (owner only)
- `GET /api/v1/users/me/channels` - List channels owned by current user

### Subscriptions (Channel-based)

**Channel Subscriptions (Require Authentication):**
- `POST /api/v1/channels/{id}/subscribe` - Subscribe to a channel
- `DELETE /api/v1/channels/{id}/subscribe` - Unsubscribe from a channel
- `GET /api/v1/users/me/subscriptions` - List channels I'm subscribed to
- `GET /api/v1/videos/subscriptions` - List videos from subscribed channels

**Legacy User Subscriptions (Deprecated):**
- `POST /api/v1/users/{id}/subscribe` - Subscribe to user's default channel
- `DELETE /api/v1/users/{id}/subscribe` - Unsubscribe from user's default channel

Notes:
- Subscriptions are now channel-based for PeerTube compatibility
- User subscription endpoints maintained for backward compatibility
- Channel payloads include `subscriber_count`

### Comments

**Public Endpoints:**
- `GET /api/v1/videos/{id}/comments` - List comments for a video (threaded, paginated)

**Protected Endpoints (Require Authentication):**
- `POST /api/v1/videos/{id}/comments` - Add a comment to a video
- `GET /api/v1/comments/{id}` - Get a specific comment
- `PUT /api/v1/comments/{id}` - Update comment (owner only)
- `DELETE /api/v1/comments/{id}` - Delete comment (owner/moderator)
- `POST /api/v1/comments/{id}/flag` - Flag comment for moderation
- `DELETE /api/v1/comments/{id}/flag` - Remove flag from comment
- `POST /api/v1/comments/{id}/moderate` - Moderate comment (video owner/admin)

### Ratings

**Protected Endpoints (Require Authentication):**
- `PUT /api/v1/videos/{id}/rating` - Set video rating (like/dislike/none)
- `GET /api/v1/videos/{id}/rating` - Get user's rating for a video
- `GET /api/v1/users/me/ratings` - List all user's video ratings

### Playlists

**Public Endpoints:**
- `GET /api/v1/playlists/{id}` - Get playlist details (if public)
- `GET /api/v1/playlists/{id}/items` - List playlist items (if public)

**Protected Endpoints (Require Authentication):**
- `GET /api/v1/playlists` - List user's playlists
- `POST /api/v1/playlists` - Create new playlist
- `PUT /api/v1/playlists/{id}` - Update playlist (owner only)
- `DELETE /api/v1/playlists/{id}` - Delete playlist (owner only)
- `POST /api/v1/playlists/{id}/items` - Add video to playlist
- `DELETE /api/v1/playlists/{id}/items/{itemId}` - Remove video from playlist
- `PUT /api/v1/playlists/{id}/items/{itemId}/position` - Reorder playlist items
- `GET /api/v1/users/me/playlists/watch-later` - Get Watch Later playlist
- `POST /api/v1/users/me/playlists/watch-later` - Add to Watch Later

### Captions/Subtitles

**Public Endpoints:**
- `GET /api/v1/videos/{id}/captions` - List available captions for a video
- `GET /api/v1/videos/{id}/captions/{captionId}/content` - Download caption file

**Protected Endpoints (Require Authentication):**
- `POST /api/v1/videos/{id}/captions` - Upload caption file (owner only)
- `PUT /api/v1/videos/{id}/captions/{captionId}` - Update caption metadata
- `DELETE /api/v1/videos/{id}/captions/{captionId}` - Delete caption

### Messages

**Standard Messages (Require Authentication):**
- `POST /api/v1/messages` - Send a message to another user
- `GET /api/v1/messages` - Get messages (optionally filtered by conversationId)
- `PUT /api/v1/messages/{messageId}/read` - Mark message as read
- `DELETE /api/v1/messages/{messageId}` - Delete a message

### Conversations

**Conversation Management (Require Authentication):**
- `GET /api/v1/conversations` - Get user's conversation list
- `GET /api/v1/conversations/unread-count` - Get total count of unread messages

### Notifications

**Notification Management (Require Authentication):**
- `GET /api/v1/notifications` - Get user's notifications
  - Query Parameters:
    - `limit` (integer): Max results per page (1-100, default: 50)
    - `offset` (integer): Pagination offset (default: 0)
    - `unread` (boolean): Filter to unread notifications only
- `GET /api/v1/notifications/unread-count` - Get count of unread notifications
- `GET /api/v1/notifications/stats` - Get notification statistics
  - Returns total count, unread count, and breakdown by notification type
- `PUT /api/v1/notifications/{id}/read` - Mark notification as read
- `PUT /api/v1/notifications/read-all` - Mark all notifications as read
- `DELETE /api/v1/notifications/{id}` - Delete a notification

**Notification Types:**
- `new_video` - New video from subscribed channel
- `video_processed` - Your video finished processing
- `video_failed` - Your video failed processing
- `new_subscriber` - Someone subscribed to your channel
- `comment` - Comment on your video
- `mention` - You were mentioned
- `new_message` - New message received
- `message_read` - Message read receipt (optional)
- `system` - System announcement

**End-to-End Encrypted Messages:**
- `POST /api/v1/e2ee/setup` - Setup E2EE with master key (requires auth)
- `POST /api/v1/e2ee/unlock` - Unlock E2EE session (requires auth)
- `POST /api/v1/e2ee/key-exchange` - Exchange keys for conversation (requires auth)
- `POST /api/v1/messages/secure` - Send encrypted message (requires auth)

### Users

**Public Endpoints:**
- `GET /api/v1/users/{id}` - Get user profile
- `GET /api/v1/users/{id}/videos` - Get user's public videos

**Protected Endpoints (Require Authentication):**
- `POST /api/v1/users` - Create a new user (admin only)
- `GET /api/v1/users/me` - Get current user profile
- `PUT /api/v1/users/me` - Update current user profile
- `POST /api/v1/users/me/avatar` - Upload avatar image (supports PNG, JPEG, WebP, GIF, HEIC, TIFF)
- `GET /api/v1/users/me/subscriptions` - List channels you're subscribed to
- `POST /api/v1/users/{id}/subscribe` - Subscribe to a user
- `DELETE /api/v1/users/{id}/subscribe` - Unsubscribe from a user

### Moderation & Admin

**Abuse Reports (Require Authentication):**
- `POST /api/v1/abuse-reports` - Create an abuse report for content or users
  - Report types: video, comment, user, channel
  - Statuses: pending, accepted, rejected, investigating

**Admin-Only Endpoints:**
- `GET /api/v1/admin/abuse-reports` - List all abuse reports
  - Supports filtering by status, entity type, reporter, and date range
  - Pagination with limit/offset
- `GET /api/v1/admin/abuse-reports/{id}` - Get specific abuse report details
- `PUT /api/v1/admin/abuse-reports/{id}` - Update abuse report status
  - Add moderator notes, change status to investigating/accepted/rejected
- `DELETE /api/v1/admin/abuse-reports/{id}` - Delete an abuse report

**Blocklist Management (Admin Only):**
- `POST /api/v1/admin/blocklist` - Add entry to blocklist
  - Block types: email, domain, IP, user
  - Supports expiration dates and reason tracking
- `GET /api/v1/admin/blocklist` - List all blocklist entries
  - Filter by type, active status, expiration
- `PUT /api/v1/admin/blocklist/{id}` - Update blocklist entry
  - Activate/deactivate, modify expiration
- `DELETE /api/v1/admin/blocklist/{id}` - Remove blocklist entry

### Instance Management

**Public Instance Information:**
- `GET /api/v1/instance/about` - Get instance information
  - Returns: name, description, version, statistics
  - Total users, videos, storage usage
  - Contact information, rules, supported languages
- `GET /oembed` - oEmbed endpoint for video embedding
  - Supports JSON and XML formats
  - Query params: url (required), format, maxwidth, maxheight
  - Returns standardized oEmbed response for video players

**Instance Configuration (Admin Only):**
- `GET /api/v1/admin/instance/config` - List all instance configurations
- `GET /api/v1/admin/instance/config/{key}` - Get specific configuration
- `PUT /api/v1/admin/instance/config/{key}` - Update configuration value
  - Supports public/private settings
  - JSON values for complex configuration
  - Examples: instance_name, upload_limits, signup_enabled
- `DELETE /api/v1/admin/instance/config/{key}` - Remove configuration

**Instance Statistics:**
The instance about endpoint provides real-time statistics including:
- Total registered users
- Total videos (local and federated)
- Total views across all videos
- Storage usage metrics
- Active moderation reports
- Default NSFW policy
- Signup status

## End-to-End Encrypted Messaging (E2EE)

Athena provides military-grade end-to-end encryption for secure messaging with zero-knowledge architecture.

### Security Features

- **Zero-Knowledge Architecture**: Server cannot decrypt message content
- **Perfect Forward Secrecy**: Compromised keys don't affect past/future messages  
- **Post-Compromise Security**: Fresh key exchanges restore security after compromise
- **Industry-Standard Cryptography**:
  - X25519 ECDH for key exchange
  - XChaCha20-Poly1305 AEAD for message encryption
  - Ed25519 for digital signatures
  - Argon2id for password-based key derivation
- **Session Management**: Time-limited unlocked sessions with automatic lock
- **Comprehensive Audit Logging**: Security events tracked for compliance

### How It Works

1. **Setup**: User creates master key encrypted with password using Argon2id
2. **Unlock**: User unlocks E2EE session with password (15-minute timeout)
3. **Key Exchange**: Users exchange public keys for secure conversation
4. **Secure Messaging**: Messages encrypted with per-conversation keys
5. **Message Authentication**: Ed25519 signatures prevent tampering

### API Flow

```bash
# 1. Setup E2EE for user
curl -X POST /api/v1/e2ee/setup \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"password": "strong-password"}'

# 2. Unlock E2EE session  
curl -X POST /api/v1/e2ee/unlock \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"password": "strong-password"}'

# 3. Exchange keys with recipient
curl -X POST /api/v1/e2ee/key-exchange \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"recipient_id": "user-uuid"}'

# 4. Send encrypted message
curl -X POST /api/v1/messages/secure \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "recipient_id": "user-uuid",
    "encrypted_content": "[Encrypted]Hello World",
    "pgp_signature": "signature..."
  }'
```

### Database Schema

The E2EE system uses dedicated tables:
- `user_master_keys` - Password-encrypted master keys
- `conversation_keys` - Per-conversation encryption keys
- `key_exchange_messages` - Key exchange handshakes
- `user_signing_keys` - Ed25519 signing key pairs
- `crypto_audit_log` - Security event audit trail

### Security Documentation

See [SECURITY_E2EE.md](SECURITY_E2EE.md) for comprehensive security documentation including:
- Detailed cryptographic specifications
- Threat model and security analysis
- Penetration testing guidelines
- Incident response procedures
- Compliance standards (SOC 2, FIPS 140-2)

## Architecture

```
/cmd/server            # Application entry point
/internal/
  ├── config/         # Configuration management
  ├── crypto/         # E2EE cryptographic operations
  ├── domain/         # Domain models and errors
  ├── generated/      # OpenAPI generated types
  ├── httpapi/        # HTTP handlers and routes
  ├── middleware/     # HTTP middleware (auth, CORS, rate limit)
  ├── repository/     # Database repositories
  ├── testutil/       # Test utilities
  └── usecase/        # Business logic interfaces
/api/                 # OpenAPI specifications
/migrations/          # Database migrations
/SECURITY_E2EE.md     # E2EE security documentation
```

### Notification System

The notification system provides real-time updates to users about important events:

**Automatic Notifications:**
- **Video Uploads**: Subscribers are automatically notified when channels they follow upload new public videos
- **Messages**: Users receive notifications when they get new direct messages
- **User Interactions**: Notifications for new subscribers, comments, and mentions

**Technical Implementation:**
- PostgreSQL triggers automatically create notifications for events (videos, messages)
- Notification service handles business logic and filtering
- RESTful API for managing and retrieving notifications
- Support for batch operations and pagination
- Unread count tracking and statistics

**Database Schema:**
- `notifications` table with JSONB data field for flexible notification content
- Indexes optimized for user queries and unread filtering
- Automatic cleanup of old read notifications (configurable)

## Production Deployment

For detailed production deployment instructions, see [PRODUCTION.md](./PRODUCTION.md).

### Quick Production Setup

```bash
# Build and run with Docker Compose
docker-compose -f docker-compose.prod.yml up -d

# View logs
docker-compose logs -f

# Health check
curl http://localhost:8080/health

# Stop services
docker-compose down
```

### Security Features

- **Authentication**: JWT with refresh tokens, API key support
- **Security Headers**: CSP, HSTS, X-Frame-Options, etc.
- **Rate Limiting**: Configurable per-IP and per-user limits
- **Input Validation**: Request size limits, file type validation
- **CORS**: Configurable origin restrictions
- **Encryption**: TLS support, encrypted storage for sensitive data
- **Audit Logging**: Request ID tracking, structured logs

### Environment Variables

Key environment variables (see `.env.example` for full list):

```bash
# Core Services
DATABASE_URL=postgres://user:pass@localhost:5432/athena
REDIS_URL=redis://localhost:6379/0
JWT_SECRET=your-secret-key
PORT=8080

# ATProto Federation (Optional)
ATPROTO_ENABLED=true
ATPROTO_HANDLE=your-instance.com
ATPROTO_DID=did:web:your-instance.com
BLUESKY_PDS_URL=https://bsky.social
BLUESKY_HANDLE=your-account.bsky.social
BLUESKY_APP_PASSWORD=your-app-password
BLUESKY_FIREHOSE_URL=wss://bsky.network
FEDERATION_ENABLED=true
```

## CI/CD

GitHub Actions workflows run automatically on push/PR:

1. **Test Workflow** - Runs tests, linting, and builds
2. **OpenAPI CI** - Validates API spec and generates docs

### Running CI Locally

```bash
# Run CI test pipeline
make ci-test

# Run CI build pipeline
make ci-build
```

## Database

PostgreSQL with extensions:
- `uuid-ossp` - UUID generation
- `pg_trgm` - Trigram matching for full-text search
- `unaccent` - Accent-insensitive search
- `btree_gin` - GIN index support

### Migrations

```bash
# Run migrations (requires DATABASE_URL)
make migrate-up

# Run test migrations
make migrate-test
```

**Key Migrations:**
- `014_create_messages_table.sql` - User messaging system
- `015_add_e2ee_messaging.sql` - End-to-end encryption support
- `020_create_notifications_table.sql` - Notification system with triggers
- `021_add_message_notifications.sql` - Message notification triggers

## Contributing

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

### Development Guidelines

- Follow Go best practices and conventions
- Update OpenAPI spec for API changes
- Write tests for new features
- Run `make lint` before committing
- Update documentation as needed

## Troubleshooting

### Common Issues

**Database connection errors:**
```bash
# Check if PostgreSQL is running
docker-compose ps
# Check logs
docker-compose logs postgres
```

**Port already in use:**
```bash
# Change ports in docker-compose.yml or .env
# Or stop conflicting services
```

**Tests failing:**
```bash
# Ensure test database is running
docker-compose -f docker-compose.test.yml up -d
# Check test database logs
docker-compose -f docker-compose.test.yml logs
```

## License

MIT License - see [LICENSE](LICENSE) file for details

## Acknowledgments

- Inspired by [PeerTube](https://github.com/Chocobozzz/PeerTube)
- Built with [Chi Router](https://github.com/go-chi/chi)
- Uses [SQLX](https://github.com/jmoiron/sqlx) for database operations

## Support

For issues and questions:
- Open an issue on [GitHub](https://github.com/yegamble/athena/issues)
- Check existing issues before creating new ones
- Provide detailed information for bug reports

---

**Ready to get started?** Run `make setup` and start building!
### Auth & Sessions

- Access tokens are signed JWTs (HS256) containing `sub` (user ID), `iat`, and `exp`. Default access token TTL is 15 minutes.
- Refresh tokens are opaque UUIDs persisted in Postgres and rotated on each refresh. Old tokens are revoked.
- Sessions are stored in Redis; each session is keyed by the refresh token (`sess:<refresh-token> -> <userID>`) and indexed per user (`user:sessions:<userID>`). On login and refresh, the Redis session is created/rotated; on logout, all user sessions and refresh tokens are revoked.
- Required config: `JWT_SECRET`, `DATABASE_URL`, and `REDIS_URL`. Optional `SESSION_TIMEOUT` controls Redis session TTL (default 24h); refresh token TTL defaults to 7 days.
# Regenerating OpenAPI Types

If you modify `api/openapi.yaml`, regenerate types and interfaces with:

```
scripts/gen-openapi.sh
```

Requires `oapi-codegen`:

```
go install github.com/deepmap/oapi-codegen/v2/cmd/oapi-codegen@latest
```

### Avatar WebP Optimization

Avatars uploaded via `/api/v1/users/me/avatar` are validated (PNG/JPEG MIME sniffing), saved under `storage/avatars/`, uploaded to IPFS, and pinned. If WebP encoding is enabled, a WebP variant is generated and pinned as well.

- Enable at build time: `go build -tags webp ./...`
- Configure WebP quality via `WEBP_QUALITY` (1–100). `0` uses encoder defaults.
- API exposes both `avatar_ipfs_cid` and `avatar_webp_ipfs_cid` for clients.
Pagination (preferred)

- Use `page` (1-based) and `pageSize` across list endpoints. Responses include a `meta` object with `total`, `limit`, `offset`, `page`, and `pageSize`.
- `limit`/`offset` are still accepted for backward compatibility but are deprecated. When provided, they are converted to `page`/`pageSize`.
- Examples:
  - `GET /api/v1/videos?page=2&pageSize=24`
  - `GET /api/v1/videos/search?q=go&page=1&pageSize=10`
  - `GET /api/v1/videos/subscriptions?page=1&pageSize=20`

Notes

- Trending: `GET /api/v1/trending` also accepts `page` and `pageSize` for consistency and returns these fields in `meta`; however, paging beyond the first page may be constrained by the current trending repository (no offset support).
### Notifications

**Protected Endpoints (Require Authentication):**

- `GET /api/v1/notifications` — List my notifications
  - Query: `page`, `pageSize` (preferred) or `limit`, `offset` (deprecated), `unread` (bool)
  - Response (wrapped): `{ success, data: Notification[], meta: { total, limit, offset, page, pageSize } }`
  - Example:
    - `GET /api/v1/notifications?page=1&pageSize=20`

- `GET /api/v1/notifications/unread-count` — Get my unread count
  - Response: `{ unread_count: number }`

- `GET /api/v1/notifications/stats` — Get my notification statistics
  - Response: `{ total_count, unread_count, by_type: { <type>: count } }`

- `PUT /api/v1/notifications/{id}/read` — Mark a notification as read
  - Response: `{ success: true }`

- `PUT /api/v1/notifications/read-all` — Mark all notifications as read
  - Response: `{ success: true }`

- `DELETE /api/v1/notifications/{id}` — Delete a notification
  - Response: 204 No Content
