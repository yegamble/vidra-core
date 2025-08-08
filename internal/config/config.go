package config

import (
    "os"
)

type Config struct {
    HTTPPort           string
    PostgresURL        string
    RedisAddr          string
    RedisPassword      string
    S3Endpoint         string
    S3Region           string
    S3AccessKey        string
    S3SecretKey        string
    S3Bucket           string
    S3UseSSL           bool
    IPFSPath           string // e.g. ~/.ipfs
    WalletServiceURL   string // Node wallet microservice base URL
}

func getenv(key, def string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return def
}

func Load() *Config {
    cfg := &Config{
        HTTPPort:         getenv("HTTP_PORT", "8080"),
        PostgresURL:      getenv("POSTGRES_URL", "postgres://peertube:peertube@localhost:5432/peertube?sslmode=disable"),
        RedisAddr:        getenv("REDIS_ADDR", "localhost:6379"),
        RedisPassword:    getenv("REDIS_PASSWORD", ""),
        S3Endpoint:       getenv("S3_ENDPOINT", "localhost:9000"),
        S3Region:         getenv("S3_REGION", "us-east-1"),
        S3AccessKey:      getenv("S3_ACCESS_KEY", "minioadmin"),
        S3SecretKey:      getenv("S3_SECRET_KEY", "minioadmin"),
        S3Bucket:         getenv("S3_BUCKET", "gotube"),
        S3UseSSL:         getenv("S3_USE_SSL", "false") == "true",
        IPFSPath:         getenv("IPFS_PATH", ""),
        WalletServiceURL: getenv("WALLET_SVC_URL", "http://localhost:8090"),
    }
    return cfg
}
