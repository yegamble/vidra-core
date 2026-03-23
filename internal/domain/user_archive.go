package domain

import "time"

// UserExportState represents the state of a user data export.
type UserExportState int

const (
	UserExportStatePending    UserExportState = 1
	UserExportStateProcessing UserExportState = 2
	UserExportStateCompleted  UserExportState = 3
	UserExportStateFailed     UserExportState = 4
)

// UserImportState represents the state of a user data import.
type UserImportState int

const (
	UserImportStatePending    UserImportState = 1
	UserImportStateProcessing UserImportState = 2
	UserImportStateCompleted  UserImportState = 3
	UserImportStateFailed     UserImportState = 4
)

// UserExport represents a user data export request.
type UserExport struct {
	ID        int64     `json:"id" db:"id"`
	UserID    string    `json:"userId" db:"user_id"`
	State     int       `json:"state" db:"state"`
	FilePath  string    `json:"-" db:"file_path"`
	FileSize  int64     `json:"fileSize" db:"file_size"`
	CreatedAt time.Time `json:"createdAt" db:"created_at"`
	ExpiresAt time.Time `json:"expiresAt" db:"expires_at"`
}

// UserImport represents a user data import request.
type UserImport struct {
	ID        int64     `json:"id" db:"id"`
	UserID    string    `json:"userId" db:"user_id"`
	State     int       `json:"state" db:"state"`
	CreatedAt time.Time `json:"createdAt" db:"created_at"`
}
