package backup

import (
	"errors"
	"time"
)

var (
	ErrInvalidManifest = errors.New("invalid backup manifest")
)

type Manifest struct {
	Version         int       `json:"version"`
	AppVersion      string    `json:"app_version"`
	SchemaVersion   int64     `json:"schema_version"`
	GooseVersion    string    `json:"goose_version"`
	CreatedAt       time.Time `json:"created_at"`
	Contents        []string  `json:"contents"`
	PostgresVersion string    `json:"postgres_version,omitempty"`
	Checksum        string    `json:"checksum,omitempty"`
}

func NewManifest(appVersion string, schemaVersion int64) *Manifest {
	return &Manifest{
		Version:       1,
		AppVersion:    appVersion,
		SchemaVersion: schemaVersion,
		GooseVersion:  "v3.x.x",
		CreatedAt:     time.Now().UTC(),
		Contents:      []string{"database.sql", "redis.rdb", "storage/"},
	}
}

func (m *Manifest) Validate() error {
	if m.AppVersion == "" {
		return ErrInvalidManifest
	}

	if m.SchemaVersion < 0 {
		return ErrInvalidManifest
	}

	if len(m.Contents) == 0 {
		return ErrInvalidManifest
	}

	return nil
}
