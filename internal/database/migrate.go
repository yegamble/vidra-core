package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"

	"athena/migrations"

	"github.com/jmoiron/sqlx"
	"github.com/pressly/goose/v3"
)

var setBaseFSOnce sync.Once

func RunMigrations(ctx context.Context, db *sqlx.DB) error {
	setBaseFSOnce.Do(func() {
		goose.SetBaseFS(migrations.FS)
	})

	sqlDB := db.DB

	if err := goose.UpContext(ctx, sqlDB, "."); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	log.Println("Database migrations completed successfully")
	return nil
}

func CurrentVersion(db *sqlx.DB) (int64, error) {
	sqlDB := db.DB
	version, err := goose.GetDBVersion(sqlDB)
	if err != nil {
		return 0, fmt.Errorf("failed to get current migration version: %w", err)
	}
	return version, nil
}

func RunMigrationsWithDB(ctx context.Context, db *sql.DB) error {
	setBaseFSOnce.Do(func() {
		goose.SetBaseFS(migrations.FS)
	})

	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	log.Println("Database migrations completed successfully")
	return nil
}
