package domain

import (
	"errors"
	"time"
)

// Studio job sentinel errors.
var (
	ErrInvalidStudioTask   = errors.New("invalid studio task")
	ErrStudioJobNotFound   = errors.New("studio job not found")
	ErrStudioJobInProgress = errors.New("studio editing job already in progress for this video")
)

// StudioJobStatus represents the processing state of a studio editing job.
type StudioJobStatus string

const (
	StudioJobStatusPending    StudioJobStatus = "pending"
	StudioJobStatusProcessing StudioJobStatus = "processing"
	StudioJobStatusCompleted  StudioJobStatus = "completed"
	StudioJobStatusFailed     StudioJobStatus = "failed"
)

// ValidStudioTaskNames enumerates the task types accepted by the studio editor.
var ValidStudioTaskNames = map[string]bool{
	"cut":           true,
	"add-intro":     true,
	"add-outro":     true,
	"add-watermark": true,
}

// StudioTaskOptions holds the optional parameters for a studio editing task.
type StudioTaskOptions struct {
	Start *float64 `json:"start,omitempty" db:"start_seconds"`
	End   *float64 `json:"end,omitempty"   db:"end_seconds"`
	File  string   `json:"file,omitempty"  db:"file_path"`
}

// StudioTask represents a single editing operation in a studio edit request.
type StudioTask struct {
	Name    string            `json:"name"    db:"name"`
	Options StudioTaskOptions `json:"options" db:"options"`
}

// StudioEditRequest is the inbound payload for POST /api/v1/videos/{id}/studio/edit.
type StudioEditRequest struct {
	Tasks []StudioTask `json:"tasks"`
}

// Validate checks that the edit request contains at least one valid task and
// that each task's options are internally consistent.
func (r *StudioEditRequest) Validate() error {
	if len(r.Tasks) == 0 {
		return ErrInvalidStudioTask
	}
	for _, t := range r.Tasks {
		if !ValidStudioTaskNames[t.Name] {
			return ErrInvalidStudioTask
		}
		switch t.Name {
		case "cut":
			if t.Options.Start == nil || t.Options.End == nil {
				return ErrInvalidStudioTask
			}
			if *t.Options.Start < 0 || *t.Options.End <= *t.Options.Start {
				return ErrInvalidStudioTask
			}
		case "add-intro", "add-outro", "add-watermark":
			if t.Options.File == "" {
				return ErrInvalidStudioTask
			}
		}
	}
	return nil
}

// StudioJob tracks the lifecycle of an asynchronous video editing job.
type StudioJob struct {
	ID           string          `json:"id"                      db:"id"`
	VideoID      string          `json:"videoId"                 db:"video_id"`
	UserID       string          `json:"userId"                  db:"user_id"`
	Status       StudioJobStatus `json:"status"                  db:"status"`
	Tasks        string          `json:"tasks"                   db:"tasks"` // JSON-encoded []StudioTask
	CreatedAt    time.Time       `json:"createdAt"               db:"created_at"`
	UpdatedAt    time.Time       `json:"updatedAt"               db:"updated_at"`
	CompletedAt  *time.Time      `json:"completedAt,omitempty"   db:"completed_at"`
	ErrorMessage string          `json:"errorMessage,omitempty"  db:"error_message"`
}
