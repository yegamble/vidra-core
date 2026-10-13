package moderation

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// MaxMessageLen bounds a report message body, in characters (the 0163 CHECK).
const MaxMessageLen = 3000

// ErrNoLocalReporter means a message was posted to a report another instance
// filed (a remote Flag, 0153): there is no local reporter to deliver it to, so
// its thread is read-only.
var ErrNoLocalReporter = errors.New("moderation: report has no local reporter to message")

// Message is one entry in a report's thread (migration 0163).
type Message struct {
	ID uuid.UUID
	// AuthorID is uuid.Nil, and AuthorUsername "", once the author's account
	// was deleted.
	AuthorID       uuid.UUID
	AuthorUsername string
	// ByModerator is the side of the thread: true when the author was not the
	// report's reporter, frozen at write time.
	ByModerator bool
	Body        string
	CreatedAt   time.Time
}

// Thread is one page of a report's messages, oldest first.
type Thread struct {
	Messages []Message
	Total    int64
	// CanReply is false for a remote Flag report: staff may read its thread,
	// but nobody can write to it.
	CanReply bool
}

// threadSide resolves callerID's side of reportID's thread: the report's local
// reporter writes as the reporter (byModerator false), staff who are not the
// reporter as moderators. Anyone else gets ErrNotFound, exactly like an
// unknown report, so the thread never confirms that a report exists to someone
// who may not see it. remote reports a thread with no local reporter.
func (s *Service) threadSide(ctx context.Context, reportID, callerID uuid.UUID, staff bool) (byModerator, remote bool, err error) {
	reporter, err := s.repo.GetReportReporterID(ctx, reportID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, false, ErrNotFound
	case err != nil:
		return false, false, err
	case reporter.Valid && uuid.UUID(reporter.Bytes) == callerID:
		return false, false, nil
	case staff:
		return true, !reporter.Valid, nil
	}
	return false, false, ErrNotFound
}

// Messages returns a page of reportID's thread for callerID (staff says whether
// the caller holds a moderation role). See threadSide for who may read it. The
// caller clamps limit/offset.
func (s *Service) Messages(ctx context.Context, reportID, callerID uuid.UUID, staff bool, limit, offset int32) (Thread, error) {
	_, remote, err := s.threadSide(ctx, reportID, callerID, staff)
	if err != nil {
		return Thread{}, err
	}
	rows, err := s.repo.ListReportMessages(ctx, sqlcgen.ListReportMessagesParams{
		ReportID: reportID, ResultLimit: limit, ResultOffset: offset,
	})
	if err != nil {
		return Thread{}, err
	}
	total, err := s.repo.CountReportMessages(ctx, reportID)
	if err != nil {
		return Thread{}, err
	}
	th := Thread{Messages: make([]Message, 0, len(rows)), Total: total, CanReply: !remote}
	for _, r := range rows {
		th.Messages = append(th.Messages, Message{ID: r.ID, AuthorID: uuid.UUID(r.AuthorID.Bytes),
			AuthorUsername: r.AuthorUsername, ByModerator: r.IsModerator, Body: r.Body, CreatedAt: r.CreatedAt})
	}
	return th, nil
}

// PostMessage appends body (already validated) to reportID's thread as
// callerID. Access is Messages'; a remote Flag report is ErrNoLocalReporter.
func (s *Service) PostMessage(ctx context.Context, reportID, callerID uuid.UUID, staff bool, body string) (Message, error) {
	byModerator, remote, err := s.threadSide(ctx, reportID, callerID, staff)
	if err != nil {
		return Message{}, err
	}
	if remote {
		return Message{}, ErrNoLocalReporter
	}
	row, err := s.repo.CreateReportMessage(ctx, sqlcgen.CreateReportMessageParams{
		ReportID: reportID, AuthorID: callerID, IsModerator: byModerator, Body: body,
	})
	if err != nil {
		return Message{}, err
	}
	return Message{ID: row.ID, AuthorID: callerID, ByModerator: byModerator, Body: body, CreatedAt: row.CreatedAt}, nil
}
