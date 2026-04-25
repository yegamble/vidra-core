package payments

import (
	"context"

	"vidra-core/internal/repository"

	"github.com/google/uuid"
)

// RepoChannelLookup adapts repository.ChannelRepository to the ChannelLookup
// interface used by LedgerService. Resolves a channel UUID string to its
// owner (account_id) UUID string, or "" when not found / not a valid UUID.
type RepoChannelLookup struct {
	repo *repository.ChannelRepository
}

// NewRepoChannelLookup constructs a lookup backed by a channel repository.
func NewRepoChannelLookup(repo *repository.ChannelRepository) *RepoChannelLookup {
	return &RepoChannelLookup{repo: repo}
}

// ResolveOwner returns the account_id (owner user UUID) of a channel, or ""
// if the channel is missing, deleted, or the ID fails to parse.
// Non-existence is not an error — LedgerService treats this as "no recipient
// tip_in entry" and only records tip_out.
func (l *RepoChannelLookup) ResolveOwner(ctx context.Context, channelID string) (string, error) {
	if l == nil || l.repo == nil {
		return "", nil
	}
	id, err := uuid.Parse(channelID)
	if err != nil {
		return "", nil
	}
	ch, err := l.repo.GetByID(ctx, id)
	if err != nil {
		return "", nil // missing channel is not an error for ledger purposes
	}
	if ch == nil {
		return "", nil
	}
	return ch.AccountID.String(), nil
}
