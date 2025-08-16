Postman: Athena Auth API

Overview
- Collection: `postman/athena-auth.postman_collection.json`
- Environment (template): `postman/athena.local.postman_environment.json`

Usage
- Import the collection and the environment into Postman.
- Select the `Athena Local` environment. Ensure `baseUrl` points to your running server (default `http://localhost:8080`).
- Run requests in order for a happy-path flow:
  1) Register
  2) Login
  3) Refresh
  4) Logout
  5) Refresh After Logout (expects 401)
- Negative cases are provided: Invalid Login (401), Refresh Missing Token (400), Refresh Invalid Token (401), Logout Without Token (401).

Videos API
The collection includes comprehensive tests for the video CRUD API:

## Video Listing & Search
- **List Videos**: `GET {{baseUrl}}/api/v1/videos` - Lists public videos with pagination and filtering
  - Query params: `limit`, `offset`, `sort`, `order`, `category`, `language`
  - Supports sorting by `upload_date`, `views`, `duration`, `title`
- **List Videos - With Filters**: Tests category and language filtering
- **Search Videos**: `GET {{baseUrl}}/api/v1/videos/search` - Full-text search with relevance ranking
  - Query params: `q` (required), `tags[]`, `category`, `language`, `sort`, `order`
  - Supports relevance-based sorting when searching
- **Search Videos - With Tags**: Tests multi-tag filtering and search combinations
- **Search Videos - Missing Query (400)**: Validates search query requirement

## Video CRUD Operations
- **Get Video by ID**: `GET {{baseUrl}}/api/v1/videos/{{video_id}}` - Retrieves video details
- **Get Video - Not Found (404)**: Tests non-existent video handling
- **Create Video**: `POST {{baseUrl}}/api/v1/videos` with `Authorization: Bearer {{access_token}}`
  - Response contains `id` and `thumbnail_id` as UUIDs and sets `Location` header
  - Sets `video_id` environment variable for subsequent tests
- **Create Video - Missing Token (401)**: Verifies auth middleware rejection
- **Create Video - Invalid JSON (400)**: Tests malformed request handling
- **Create Video - Missing Required Fields (400)**: Validates required field enforcement
- **Update Video**: `PUT {{baseUrl}}/api/v1/videos/{{video_id}}` - Updates video metadata
  - Requires authentication and ownership validation
  - Tests successful update with changed privacy, category, etc.
- **Update Video - Not Found (404)**: Tests updating non-existent video
- **Update Video - Invalid JSON (400)**: Tests malformed update requests
- **Update Video - Missing Token (401)**: Verifies auth requirement
- **Delete Video**: `DELETE {{baseUrl}}/api/v1/videos/{{video_id}}` - Removes video
  - Requires authentication and ownership validation
  - Returns 204 No Content on success
- **Delete Video - Not Found (404)**: Tests deleting non-existent video
- **Delete Video - Missing Token (401)**: Verifies auth requirement

## User Videos
- **Get User Videos**: `GET {{baseUrl}}/api/v1/users/{{user_id}}/videos` - Lists videos by user
  - Supports pagination with `limit` and `offset`
  - Validates all returned videos belong to the specified user
- **Get User Videos - Nonexistent User**: Tests behavior for non-existent users

## Video Streaming & Upload
- **Video Streaming Endpoint**: `GET {{baseUrl}}/api/v1/videos/{{video_id}}/stream` - HLS streaming
  - Query params: `quality` (optional, defaults to 720p). Supported values: 240p, 360p, 480p, 720p, 1080p, 1440p, 2160p, 4320p
  - Returns HLS playlist with proper Content-Type header
- **Upload Video Chunk**: `POST {{baseUrl}}/api/v1/videos/{{video_id}}/upload` - Chunked video upload
  - Requires headers: `X-Chunk-Index`, `X-Total-Chunks`, `X-Chunk-Checksum`
  - Tests successful chunk upload with proper validation
- **Upload Video Chunk - Missing Headers (400)**: Validates required upload headers
- **Complete Video Upload**: `POST {{baseUrl}}/api/v1/videos/{{video_id}}/complete` - Finalize upload
  - Queues video for processing after all chunks are uploaded

Notes
- Register pre-request script auto-generates a unique `username` and `email` if not set.
- Tests capture `access_token`, `refresh_token`, and `user_id` into environment variables for reuse.
- Logout uses `Authorization: Bearer {{access_token}}`.

Run with Newman
- Prereqs: Docker and docker-compose installed.
- Quick run against an already-running server:
  - `make postman-newman` (uses `BASE_URL=http://localhost:8080` by default)
  - Override base URL: `make postman-newman BASE_URL=http://localhost:18080`
- End-to-end spin-up + run + teardown:
  - `make postman-e2e` (starts Postgres/Redis/app via `docker-compose.test.yml`, runs Newman, then tears down)
- JUnit results written to `postman/newman-results.xml` for CI consumption.
