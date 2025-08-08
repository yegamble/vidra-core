package config

import (
    "os"
)

// Config holds all runtime configuration values for the application.  Each
// field is populated from the process environment with a sensible default.
// See .env.example for examples of these variables.
type Config struct {
    HTTPPort         string
    PostgresURL      string
    RedisAddr        string
    RedisPassword    string
    S3Endpoint       string
    S3Region         string
    S3AccessKey      string
    S3SecretKey      string
    S3Bucket         string
    S3UseSSL         bool
    IPFSPath         string // e.g. http://kubo:5001 for remote API or empty for local
    WalletServiceURL string // Node wallet microservice base URL
}

// getenv returns the value of an environment variable or a fallback default
// if the variable is not set.
func getenv(key, def string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return def
}

// Load populates a Config struct from environment variables.  Defaults are
// chosen to make local development seamless when using docker compose.
func Load() *Config {
    cfg := &Config{
        HTTPPort:         getenv("HTTP_PORT", "8080"),
        PostgresURL:      getenv("POSTGRES_URL", "postgres://peertube:peertube@postgres:5432/peertube?sslmode=disable"),
        RedisAddr:        getenv("REDIS_ADDR", "redis:6379"),
        RedisPassword:    getenv("REDIS_PASSWORD", ""),
        S3Endpoint:       getenv("S3_ENDPOINT", "minio:9000"),
        S3Region:         getenv("S3_REGION", "us-east-1"),
        S3AccessKey:      getenv("S3_ACCESS_KEY", "minioadmin"),
        S3SecretKey:      getenv("S3_SECRET_KEY", "minioadmin"),
        S3Bucket:         getenv("S3_BUCKET", "gotube"),
        S3UseSSL:         getenv("S3_USE_SSL", "false") == "true",
        IPFSPath:         getenv("IPFS_PATH", ""),
        WalletServiceURL: getenv("WALLET_SVC_URL", "http://wallet-svc:8090"),
    }
    return cfg
}