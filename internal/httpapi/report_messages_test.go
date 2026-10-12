package httpapi

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/notification"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// modMessageRow mirrors a report_messages row (0163) in the moderation fake.
type modMessageRow struct {
	id, reportID, authorID uuid.UUID
	byModerator            bool
	body                   string
	createdAt              time.Time
}

func (f *moderationFakeRepo) GetReportReporterID(_ context.Context, id uuid.UUID) (pgtype.UUID, error) {
	r, ok := f.reportByID(id)
	if !ok {
		return pgtype.UUID{}, pgx.ErrNoRows
	}
	// A remote Flag report stores no reporter_id (0153).
	return pgtype.UUID{Bytes: r.reporterID, Valid: r.remoteActor == ""}, nil
}

func (f *moderationFakeRepo) CreateReportMessage(_ context.Context, a sqlcgen.CreateReportMessageParams) (sqlcgen.CreateReportMessageRow, error) {
	m := modMessageRow{id: uuid.New(), reportID: a.ReportID, authorID: a.AuthorID, byModerator: a.IsModerator,
		body: a.Body, createdAt: time.Now()}
	f.messages = append(f.messages, m)
	return sqlcgen.CreateReportMessageRow{ID: m.id, CreatedAt: m.createdAt}, nil
}

func (f *moderationFakeRepo) ListReportMessages(_ context.Context, a sqlcgen.ListReportMessagesParams) ([]sqlcgen.ListReportMessagesRow, error) {
	var rows []sqlcgen.ListReportMessagesRow
	for _, m := range f.messages { // insertion order = oldest first
		if m.reportID != a.ReportID {
			continue
		}
		row := sqlcgen.ListReportMessagesRow{ID: m.id, AuthorID: pgtype.UUID{Bytes: m.authorID, Valid: true},
			IsModerator: m.byModerator, Body: m.body, CreatedAt: m.createdAt}
		for _, u := range f.auth.users {
			if u.ID == m.authorID {
				row.AuthorUsername = u.Username
			}
		}
		rows = append(rows, row)
	}
	rows = rows[min(int(a.ResultOffset), len(rows)):]
	return rows[:min(int(a.ResultLimit), len(rows))], nil
}

func (f *moderationFakeRepo) CountReportMessages(ctx context.Context, reportID uuid.UUID) (int64, error) {
	rows, err := f.ListReportMessages(ctx, sqlcgen.ListReportMessagesParams{ReportID: reportID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

// NotifyReportMessage mirrors the shape of the real set-based fan-out (0163)
// closely enough to prove the HTTP wiring: a moderator's message reaches the
// local reporter with no actor, the reporter's reaches every active staff
// member, never the author, honouring the report_message preference and one
// notification per (user, message). The statement itself is proved in
// store.TestReportMessageFanOutOnRealPG.
func (f *notifFakeRepo) NotifyReportMessage(_ context.Context, messageID uuid.UUID) (int64, error) {
	if f.createErr != nil {
		return 0, f.createErr
	}
	var msg modMessageRow
	for _, m := range f.reports.messages {
		if m.id == messageID {
			msg = m
		}
	}
	r, ok := f.reports.reportByID(msg.reportID)
	if !ok {
		return 0, nil
	}
	var n int64
	for _, u := range f.auth.users {
		recipient := u.Role == "admin" || u.Role == "moderator"
		if msg.byModerator {
			recipient = r.remoteActor == "" && u.ID == r.reporterID
		}
		if !recipient || u.ID == msg.authorID || !u.IsActive || u.DeletedAt.Valid {
			continue
		}
		if enabled, ok := f.prefs[notifPrefKey(u.ID, notification.TypeReportMessage)]; ok && !enabled {
			continue
		}
		f.notifs = append(f.notifs, sqlcgen.Notification{
			ID: uuid.New(), UserID: u.ID, Type: notification.TypeReportMessage,
			ActorID:         pgtype.UUID{Bytes: msg.authorID, Valid: !msg.byModerator},
			ReportID:        pgtype.UUID{Bytes: r.id, Valid: true},
			ReportMessageID: pgtype.UUID{Bytes: msg.id, Valid: true},
			CreatedAt:       time.Now(),
		})
		n++
	}
	return n, nil
}
