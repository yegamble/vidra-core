package peertubeimport

import (
	"context"
	"fmt"
	"time"
)

// PeerTube calls these blocklists, but personal entries hide content; they do
// not suspend an account or impose Vidra's symmetric direct-message block.
type sourcePersonalMute struct {
	ID, OwnerUserID, TargetUserID int64
	Domain                        string
	CreatedAt                     time.Time
}

func (s *Source) personalMutes(ctx context.Context, instance bool) ([]sourcePersonalMute, error) {
	table := "accountBlocklist"
	query := `SELECT b.id, owner."userId", target."userId", '', b."createdAt"
 FROM "accountBlocklist" b
 JOIN account owner ON owner.id=b."accountId"
 JOIN account target ON target.id=b."targetAccountId"
 WHERE owner."userId" IS NOT NULL AND target."userId" IS NOT NULL ORDER BY b.id`
	if instance {
		table = "serverBlocklist"
		query = `SELECT b.id, owner."userId", 0, target.host, b."createdAt"
  FROM "serverBlocklist" b JOIN account owner ON owner.id=b."accountId"
  JOIN server target ON target.id=b."targetServerId"
  WHERE owner."userId" IS NOT NULL ORDER BY b.id`
	}
	exists, err := s.tableExists(ctx, table)
	if err != nil || !exists {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: read personal mutes: %w", err)
	}
	defer rows.Close()
	var result []sourcePersonalMute
	for rows.Next() {
		var m sourcePersonalMute
		if err := rows.Scan(&m.ID, &m.OwnerUserID, &m.TargetUserID, &m.Domain, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("peertubeimport: scan personal mute: %w", err)
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
