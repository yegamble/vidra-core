package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port         int
	DatabaseURL  string
	RedisURL     string
	IPFSApi      string
	IPFSCluster  string
	IOTANodeURL  string
	FFMPEGPath   string
	JWTSecret    string
	EnableIOTA   bool
	EnableIPFS   bool
	EnableS3     bool
	S3Endpoint   string
	S3Bucket     string
	S3AccessKey  string
	S3SecretKey  string
}

func Load() (*Config, error) {
	cfg := &Config{}

	port := flag.Int("port", 8080, "Server port")
	flag.Parse()

	cfg.Port = *port
	if envPort := os.Getenv("PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil {
			cfg.Port = p
		}
	}

	cfg.DatabaseURL = getEnvOrDefault("DATABASE_URL", "")
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	cfg.RedisURL = getEnvOrDefault("REDIS_URL", "")
	if cfg.RedisURL == "" {
		return nil, fmt.Errorf("REDIS_URL is required")
	}

	cfg.IPFSApi = getEnvOrDefault("IPFS_API", "")
	if cfg.IPFSApi == "" {
		return nil, fmt.Errorf("IPFS_API is required")
	}

	cfg.IPFSCluster = getEnvOrDefault("IPFS_CLUSTER_API", "")
	cfg.IOTANodeURL = getEnvOrDefault("IOTA_NODE_URL", "")
	cfg.FFMPEGPath = getEnvOrDefault("FFMPEG_PATH", "ffmpeg")

	cfg.JWTSecret = getEnvOrDefault("JWT_SECRET", "")
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}

	cfg.EnableIOTA = getBoolEnv("ENABLE_IOTA", false)
	cfg.EnableIPFS = getBoolEnv("ENABLE_IPFS_CLUSTER", true)
	cfg.EnableS3 = getBoolEnv("ENABLE_S3", false)

	cfg.S3Endpoint = getEnvOrDefault("S3_ENDPOINT", "")
	cfg.S3Bucket = getEnvOrDefault("S3_BUCKET", "")
	cfg.S3AccessKey = getEnvOrDefault("S3_ACCESS_KEY", "")
	cfg.S3SecretKey = getEnvOrDefault("S3_SECRET_KEY", "")

	return cfg, nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getBoolEnv(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return strings.ToLower(value) == "true" || value == "1"
}