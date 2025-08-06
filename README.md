# GoTube

GoTube is a decentralized, self‑hostable video sharing platform written in Go. It aims to provide a lightweight alternative to PeerTube with native IPFS and IOTA integration. This repository contains a monolithic backend API designed following clean architecture principles. Key features include:

- **User management**: Full authentication with registration, email verification, login/logout using JWT and Redis sessions.
- **Video uploads**: Users can upload videos via a REST API. Files are stored locally and pinned to IPFS on upload.
- **Transcoding pipeline**: Uploaded videos are asynchronously transcoded into multiple resolutions (240p–8K) using FFmpeg. Jobs are queued in Redis and processed by a background worker.
- **Decentralized storage & ledger**: Video files are backed up on IPFS. A stubbed IOTA service demonstrates how metadata could be anchored to the Tangle.
- **REST API**: Endpoints for authentication and video management are exposed via a chi router. Middleware enforces JWT authentication on protected routes.
- **Docker & Compose**: The project includes a Dockerfile and `docker-compose.yml` to run MySQL, Redis, IPFS and the Go service together. A migration script sets up the database schema.
- **GitHub Actions**: Continuous integration runs tests, vetting and builds a Docker image on every push or pull request.

## Running locally

### Prerequisites

- [Docker](https://www.docker.com/) and [Docker Compose](https://docs.docker.com/compose/)
- [Go 1.20+](https://go.dev/dl/) if you intend to build outside Docker

### Using Docker Compose

Start all services:

```bash
cd gotube
docker-compose -f build/docker-compose.yml up --build
```

This will start MySQL, Redis, an IPFS node and the GoTube API on port 8080. Environment variables in the compose file configure database credentials, JWT secret, SMTP and IOTA settings. Adjust these as needed.

### Manual setup

1. Create a MySQL database and run the schema in `scripts/migrations.sql`.
2. Run a Redis instance and an IPFS daemon (local or remote).
3. Export environment variables (`DB_DSN`, `REDIS_ADDR`, `JWT_SECRET`, etc.) as documented in `config/config.go`.
4. Build and run the server:

```bash
cd gotube
go build -o gotube ./cmd/server
./gotube
```

### API overview

- `GET /health` – health check
- `POST /auth/signup` – register a user (JSON: `{ "email": "...", "password": "..." }`)
- `POST /auth/login` – log in (JSON: `{ "email": "...", "password": "..." }`) → returns JWT token
- `POST /videos` – upload a video (multipart/form-data: `title`, `description`, `file`) – requires `Authorization: Bearer <token>` header
- `GET /videos/{id}` – get video metadata and renditions

## Notes

- This project is a prototype scaffold. The IOTA service is stubbed; integrate the official IOTA Go SDK to generate addresses and write transactions.
- Email verification is not fully implemented. A production system should generate and store verification tokens and send real emails.
- Error handling and input validation are basic; enhance these for robustness and security.
- For production deployment, configure HTTPS, rate limiting, CORS, and monitoring.