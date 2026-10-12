package federation

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// blockTable answers BlockReader from sets, the way the SQL does.
type blockTable struct {
	domains, admin, anyone map[string]bool
	owners                 map[uuid.UUID]map[string]bool
}

func (b blockTable) IsInstanceBlocked(_ context.Context, d string) (bool, error) {
	return b.domains[d], nil
}
func (b blockTable) IsRemoteActorBlockedInstanceWide(_ context.Context, a string) (bool, error) {
	return b.admin[a], nil
}
func (b blockTable) IsRemoteActorBlockedByAnyone(_ context.Context, a string) (bool, error) {
	return b.anyone[a], nil
}
func (b blockTable) IsRemoteActorBlockedBy(_ context.Context, p sqlcgen.IsRemoteActorBlockedByParams) (bool, error) {
	return b.owners[p.BlockerID][p.ActorUrl], nil
}

// A follower that arrives by import is refused by exactly what refuses a live
// Follow: its instance, an admin block, or the followed channel's owner — and
// never by somebody else's personal block.
func TestRemoteFollowerRefused(t *testing.T) {
	owner, other := uuid.New(), uuid.New()
	blocks := blockTable{
		domains: map[string]bool{"blocked.example": true},
		admin:   map[string]bool{"https://ok.example/users/banned": true},
		anyone:  map[string]bool{"https://ok.example/users/pest": true, "https://ok.example/users/fan": true},
		owners: map[uuid.UUID]map[string]bool{
			owner: {"https://ok.example/users/pest": true},
			other: {"https://ok.example/users/fan": true},
		},
	}
	for actor, want := range map[string]bool{
		"https://Blocked.example/users/anyone": true,
		"https://ok.example/users/banned":      true,
		"https://ok.example/users/pest":        true,
		"https://ok.example/users/fan":         false,
		"https://ok.example/users/stranger":    false,
	} {
		got, err := RemoteFollowerRefused(context.Background(), blocks, owner, actor)
		if err != nil || got != want {
			t.Errorf("RemoteFollowerRefused(%s) = %v, %v; want %v", actor, got, err, want)
		}
	}
}
