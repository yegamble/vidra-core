package config

import (
    "fmt"
    "os"
    "strconv"
)

// Config holds the configuration for the application. Values are loaded from
// environment variables. All fields are exported to allow other packages to
// read configuration values. If a value is not present in the environment,
// sensible defaults are used when possible. For production, be sure to set
// these values explicitly.
type Config struct {
    ServerPort string
    DB        struct {
        DSN string
    }
    Redis struct {
        Addr     string
        Password string
        DB       int
    }
    JWTSecret string
    IPFS      struct {
        APIURL string
    }
    IOTA struct {
        NodeURL string
        Seed    string
    }
    SMTP struct {
        Host     string
        Port     int
        Username string
        Password string
        From     string
    }
}

// Load reads configuration from environment variables. It returns a Config
// struct populated with values. If required values are missing, an error
// is returned. This approach centralizes configuration in one place and
// simplifies modification. The following environment variables are used:
//
//   PORT            - port on which the HTTP server listens (default: 8080)
//   DB_DSN          - Data Source Name for MySQL (e.g. user:pass@tcp(host:port)/dbname?parseTime=true)
//   REDIS_ADDR      - address of the Redis server (default: localhost:6379)
//   REDIS_PASSWORD  - password for Redis (optional)
//   REDIS_DB        - database number for Redis (default: 0)
//   JWT_SECRET      - secret key used to sign JWT tokens (required)
//   IPFS_API_URL    - URL of the IPFS API endpoint (default: http://localhost:5001)
//   IOTA_NODE_URL   - URL of the IOTA node API (optional, default: https://api.shimmer.network)
//   IOTA_SEED       - seed for generating IOTA addresses (optional, recommended to set)
//   SMTP_HOST       - SMTP server host for sending verification emails
//   SMTP_PORT       - SMTP server port
//   SMTP_USERNAME   - SMTP username
//   SMTP_PASSWORD   - SMTP password
//   SMTP_FROM       - From address used in outgoing emails
//
// You can set these variables in a .env file or via your process manager.
func Load() (*Config, error) {
    cfg := &Config{}

    // Server port
    cfg.ServerPort = getString("PORT", "8080")

    // Database
    cfg.DB.DSN = os.Getenv("DB_DSN")
    if cfg.DB.DSN == "" {
        return nil, fmt.Errorf("missing DB_DSN environment variable")
    }

    // Redis
    cfg.Redis.Addr = getString("REDIS_ADDR", "localhost:6379")
    cfg.Redis.Password = os.Getenv("REDIS_PASSWORD")
    cfg.Redis.DB = getInt("REDIS_DB", 0)

    // JWT secret
    cfg.JWTSecret = os.Getenv("JWT_SECRET")
    if cfg.JWTSecret == "" {
        return nil, fmt.Errorf("missing JWT_SECRET environment variable")
    }

    // IPFS
    cfg.IPFS.APIURL = getString("IPFS_API_URL", "http://localhost:5001")

    // IOTA
    cfg.IOTA.NodeURL = getString("IOTA_NODE_URL", "https://api.shimmer.network")
    cfg.IOTA.Seed = os.Getenv("IOTA_SEED")

    // SMTP
    cfg.SMTP.Host = os.Getenv("SMTP_HOST")
    cfg.SMTP.Port = getInt("SMTP_PORT", 0)
    cfg.SMTP.Username = os.Getenv("SMTP_USERNAME")
    cfg.SMTP.Password = os.Getenv("SMTP_PASSWORD")
    cfg.SMTP.From = os.Getenv("SMTP_FROM")

    return cfg, nil
}

// Helper to get an integer environment variable, with a default.
func getInt(key string, def int) int {
    if val, ok := os.LookupEnv(key); ok {
        i, err := strconv.Atoi(val)
        if err == nil {
            return i
        }
    }
    return def
}

// Helper to get a string environment variable, with a default.
func getString(key, def string) string {
    if val, ok := os.LookupEnv(key); ok {
        return val
    }
    return def
}