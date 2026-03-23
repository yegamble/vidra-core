package domain

import "time"

// WatchedWordList represents a list of watched words for content moderation.
// When AccountName is nil, the list applies at the server level.
type WatchedWordList struct {
	ID          int64     `json:"id" db:"id"`
	AccountName *string   `json:"accountName,omitempty" db:"account_name"` // nil = server-level
	ListName    string    `json:"listName" db:"list_name"`
	Words       []string  `json:"words" db:"-"` // stored as JSON in DB
	WordsJSON   string    `json:"-" db:"words"` // JSON string for DB
	CreatedAt   time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt   time.Time `json:"updatedAt" db:"updated_at"`
}

// CreateWatchedWordListRequest represents a request to create a watched word list.
type CreateWatchedWordListRequest struct {
	ListName string   `json:"listName" validate:"required,min=1,max=200"`
	Words    []string `json:"words" validate:"required,min=1,dive,min=1,max=100"`
}

// UpdateWatchedWordListRequest represents a request to update a watched word list.
type UpdateWatchedWordListRequest struct {
	ListName *string  `json:"listName,omitempty" validate:"omitempty,min=1,max=200"`
	Words    []string `json:"words,omitempty" validate:"omitempty,min=1,dive,min=1,max=100"`
}
