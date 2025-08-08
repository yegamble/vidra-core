package db

import (
    "github.com/jmoiron/sqlx"
    _ "github.com/lib/pq"
    "github.com/yourname/gotube/internal/config"
)

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
