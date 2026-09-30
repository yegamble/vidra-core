package peertubeimport

import (
	"context"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// A hold only removes access. Recovery/enrollment is an explicit operator action;
// source OTP removal, profile edits and repeated imports never reactivate it.
func (im *Importer) holdImportedMFAUser(ctx context.Context, id uuid.UUID, r *Report) error {
	var changed int64
	err := im.withTx(ctx, func(q *sqlcgen.Queries) error {
		var err error
		changed, err = q.ImportHoldUserForMFA(ctx, id)
		if err != nil {
			return err
		}
		if changed > 0 {
			return q.RevokeAllUserSessions(ctx, id)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if changed > 0 {
		r.count(KindUserMFAHold).Updated++
	} else {
		r.count(KindUserMFAHold).Skipped++
	}
	noteMFAHold(r)
	return nil
}

func noteMFAHold(r *Report) {
	const note = "source MFA secrets are not transferred; new source-MFA accounts are held inactive. Existing importer-owned accounts without enabled native MFA are held only during an explicit source-authoritative rerun; ordinary reruns do not repair existing accounts, and linked/native accounts are excluded. Verified operator-assisted native enrollment under restricted access is required before public access; imports never release holds"
	for _, existing := range r.Deferred {
		if existing == note {
			return
		}
	}
	r.Deferred = append(r.Deferred, note)
}
