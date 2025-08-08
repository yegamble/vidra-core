package db

import (
    "github.com/jmoiron/sqlx"
    _ "github.com/lib/pq" // postgres driver
    "github.com/yegamble/athena/internal/config"
)

// Open opens a connection to PostgreSQL using the given configuration.  It
// verifies connectivity by issuing a Ping before returning the DB handle.
func Open(cfg *config.Config) (*sqlx.DB, error) {
    db, err := sqlx.Open("postgres", cfg.PostgresURL)
    if err != nil {
        return nil, err
    }
    if err := db.Ping(); err != nil {
        return nil, err
    }
    return db, nil
}