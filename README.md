# GoTube (PeerTube-inspired Go backend)

A production-grade starter that mirrors the core backend pieces of PeerTube, rebuilt in Go with:

- Chi, SQLX, PostgreSQL, Redis
- Atlas for migrations
- S3-compatible storage via MinIO SDK (AWS, DigitalOcean Spaces, Backblaze B2)
- IPFS (Kubo RPC) for IPFS-first video storage / retrieval
- Optional IOTA wallet microservice (Node + official IOTA SDK) for premium/ad-free payments

## Quickstart

```bash
cp .env.example .env
docker compose up -d postgres redis minio kubo wallet-svc api
# create bucket 'gotube' via http://localhost:9001 (MinIO console) or let the app do it on first run

# Apply schema
export POSTGRES_URL=postgres://peertube:peertube@localhost:5432/peertube?sslmode=disable
atlas schema apply -u "$POSTGRES_URL" -f migrations/schema.hcl --dev-url "docker://postgres/16/dev"

go run ./cmd/server
```

Upload:

```bash
curl -F file=@/path/to/video.mp4 -F title="My Video" http://localhost:8080/api/v1/videos
```

Fetch:

```bash
curl http://localhost:8080/api/v1/videos/
```

## IOTA Wallet Microservice

Go currently lacks an up‑to‑date, fully supported IOTA wallet library. This starter uses the official **IOTA SDK (Node bindings)** behind a tiny HTTP service (see `wallet-svc/`). The Go API calls it for wallet creation, address retrieval, and payments.

- Start it with Docker Compose: `docker compose up -d wallet-svc`
- Configure `WALLET_SVC_URL=http://localhost:8090`

## Notes

- IPFS: We use `github.com/ipfs/kubo/client/rpc` (official replacement for go-ipfs-api) to talk to a local Kubo daemon.
- S3: We use `minio-go/v7` which is compatible with AWS S3, DigitalOcean Spaces, and Backblaze B2.
- Extend `internal/httpapi` and add more domain‑specific packages (federation, ActivityPub, transcoding queues, WebTorrent, etc.).