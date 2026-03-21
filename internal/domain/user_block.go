package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	// BlockTypeAccount and BlockTypeServer extend the existing BlockType for per-user blocks.
	BlockTypeAccount BlockType = "account"
	BlockTypeServer  BlockType = "server"
)

// UserBlock represents a per-user block of an account or entire server.
type UserBlock struct {
	ID               uuid.UUID  `db:"id"                 json:"id"`
	UserID           uuid.UUID  `db:"user_id"            json:"userId"`
	BlockType        BlockType  `db:"block_type"         json:"blockType"`
	TargetAccountID  *uuid.UUID `db:"target_account_id"  json:"targetAccountId,omitempty"`
	TargetServerHost *string    `db:"target_server_host" json:"targetServerHost,omitempty"`
	CreatedAt        time.Time  `db:"created_at"         json:"createdAt"`
}
