// Package watchword implements the moderation "watched words" list for vidra-core:
// an instance-wide set of terms a moderator/admin maintains, plus the matching/
// flagging of content against it — comment bodies on post/edit and video
// title+description on create/edit (§12). Flagging only records matches for the
// moderator review queue; it never blocks or hides the content itself (the §11
// quarantine pipeline is the hold mechanism for videos). It is HTTP-agnostic and
// testable without a server.
package watchword

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrAlreadyExists means the term is already on the list (case-insensitive).
var ErrAlreadyExists = errors.New("watchword: already exists")

// ErrMatchNotFound means the match id does not exist (triage of an unknown row).
var ErrMatchNotFound = errors.New("watchword: match not found")

// Triage states for a match, mirroring the report queue's lifecycle: a match is
// open until a moderator either acted on it (resolved) or judged it a false
// positive (dismissed). StatusAll is a QUERY value only — it is never stored.
const (
	StatusOpen      = "open"
	StatusResolved  = "resolved"
	StatusDismissed = "dismissed"
	StatusAll       = "all"
)

// ValidOutcome reports whether status is one a moderator may set (the two
// terminal states; "open" is the default a row starts in, not an outcome).
func ValidOutcome(status string) bool {
	return status == StatusResolved || status == StatusDismissed
}

// locate finds the first case-insensitive occurrence of word inside text and
// returns its offset and length in RUNES, or (-1, 0) when it is not there.
//
// Runes, not bytes: the review UI highlights the term by slicing the snapshot,
// and a byte offset would cut a multi-byte character in half. Go's ToLower maps
// rune-for-rune, so an offset counted in the lowered string is the same offset
// in the original. A (-1) result is possible in principle — Postgres lower() and
// Go's ToLower need not agree on every locale-sensitive rune — and is recorded
// honestly rather than guessed at; the queue falls back to searching the term.
func locate(text, word string) (int32, int32) {
	if word == "" {
		return -1, 0
	}
	lowerText, lowerWord := strings.ToLower(text), strings.ToLower(word)
	i := strings.Index(lowerText, lowerWord)
	if i < 0 {
		return -1, 0
	}
	return int32(utf8.RuneCountInString(lowerText[:i])), int32(utf8.RuneCountInString(lowerWord))
}

// Repository is the data access the watched-words service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreateWatchedWord(ctx context.Context, arg sqlcgen.CreateWatchedWordParams) (sqlcgen.WatchedWord, error)
	ListWatchedWords(ctx context.Context, arg sqlcgen.ListWatchedWordsParams) ([]sqlcgen.ListWatchedWordsRow, error)
	CountWatchedWords(ctx context.Context) (int64, error)
	DeleteWatchedWord(ctx context.Context, id uuid.UUID) (int64, error)
	MatchWatchedWords(ctx context.Context, text string) ([]sqlcgen.MatchWatchedWordsRow, error)
	RecordWatchedWordMatch(ctx context.Context, arg sqlcgen.RecordWatchedWordMatchParams) error
	RecordWatchedWordVideoMatch(ctx context.Context, arg sqlcgen.RecordWatchedWordVideoMatchParams) error
	ListWatchedWordMatches(ctx context.Context, arg sqlcgen.ListWatchedWordMatchesParams) ([]sqlcgen.ListWatchedWordMatchesRow, error)
	CountWatchedWordMatches(ctx context.Context, status *string) (int64, error)
	ResolveWatchedWordMatch(ctx context.Context, arg sqlcgen.ResolveWatchedWordMatchParams) (int64, error)
}

// Service holds the watched-words application logic.
type Service struct {
	repo Repository
}

// NewService builds the watched-words service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// WatchedWord is a term on the list, with who added it and when.
type WatchedWord struct {
	ID                uuid.UUID
	Word              string
	CreatedByUsername string
	CreatedAt         time.Time
}

// Add appends a term to the watched-words list. A duplicate (case-insensitive) →
// ErrAlreadyExists.
func (s *Service) Add(ctx context.Context, word string, createdBy uuid.UUID) (WatchedWord, error) {
	row, err := s.repo.CreateWatchedWord(ctx, sqlcgen.CreateWatchedWordParams{
		Word:      word,
		CreatedBy: pgconv.UUID(createdBy),
	})
	if pgconv.SQLState(err) == pgconv.SQLStateUniqueViolation { // unique violation: term already on the list
		return WatchedWord{}, ErrAlreadyExists
	}
	if err != nil {
		return WatchedWord{}, err
	}
	return WatchedWord{ID: row.ID, Word: row.Word, CreatedAt: row.CreatedAt}, nil
}

// List returns the watched words, newest first, with the total. The caller
// clamps limit/offset.
func (s *Service) List(ctx context.Context, limit, offset int32) ([]WatchedWord, int64, error) {
	rows, err := s.repo.ListWatchedWords(ctx, sqlcgen.ListWatchedWordsParams{
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountWatchedWords(ctx)
	if err != nil {
		return nil, 0, err
	}
	items := make([]WatchedWord, 0, len(rows))
	for _, r := range rows {
		item := WatchedWord{ID: r.ID, Word: r.Word, CreatedAt: r.CreatedAt}
		if r.CreatedByUsername != nil {
			item.CreatedByUsername = *r.CreatedByUsername
		}
		items = append(items, item)
	}
	return items, total, nil
}

// Delete removes a term from the list (idempotent: removing an absent term is a
// no-op).
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.repo.DeleteWatchedWord(ctx, id)
	return err
}

// FlagComment checks a comment's body against the watched-words list and records
// a match row for each term found (idempotent per word+comment), SNAPSHOTTING the
// body as it read at flag time. It returns the number of terms matched.
// Best-effort: callers invoke it as a side effect of posting a comment and must
// not fail the comment on error.
//
// On an edit the caller calls this again with the new body. A term already
// recorded for this comment hits the ON CONFLICT and keeps its ORIGINAL snapshot
// (so removing the term does not erase the evidence); a term the edit newly
// introduces creates a new match with the new body as its snapshot.
func (s *Service) FlagComment(ctx context.Context, commentID uuid.UUID, body string) (int, error) {
	matches, err := s.repo.MatchWatchedWords(ctx, body)
	if err != nil {
		return 0, err
	}
	for _, m := range matches {
		off, length := locate(body, m.Word)
		if err := s.repo.RecordWatchedWordMatch(ctx, sqlcgen.RecordWatchedWordMatchParams{
			WatchedWordID: pgconv.UUID(m.ID),
			CommentID:     pgconv.UUID(commentID),
			MatchedText:   body,
			MatchedTerm:   m.Word,
			MatchOffset:   off,
			MatchLength:   length,
		}); err != nil {
			return 0, err
		}
	}
	return len(matches), nil
}

// FlagVideo checks a video's title+description against the watched-words list
// and records a match row for each term found (idempotent per word+video), with
// the same flag-time snapshot as the comment arm. It returns the number of terms
// matched. Best-effort: callers invoke it as a side effect of creating/editing a
// video and must not fail the write on error. No auto-hold: the §11 quarantine
// pipeline is the hold mechanism for videos.
func (s *Service) FlagVideo(ctx context.Context, videoID uuid.UUID, text string) (int, error) {
	matches, err := s.repo.MatchWatchedWords(ctx, text)
	if err != nil {
		return 0, err
	}
	for _, m := range matches {
		off, length := locate(text, m.Word)
		if err := s.repo.RecordWatchedWordVideoMatch(ctx, sqlcgen.RecordWatchedWordVideoMatchParams{
			WatchedWordID: pgconv.UUID(m.ID),
			VideoID:       pgconv.UUID(videoID),
			MatchedText:   text,
			MatchedTerm:   m.Word,
			MatchOffset:   off,
			MatchLength:   length,
		}); err != nil {
			return 0, err
		}
	}
	return len(matches), nil
}

// Match target discriminators for the review queue.
const (
	MatchTargetComment = "comment"
	MatchTargetVideo   = "video"
)

// Target-status values for a match's LIVE target, as opposed to its snapshot.
// A target that was DELETED is not representable: comment_id/video_id keep their
// ON DELETE CASCADE, so the match row goes with the target (0132 records why).
const (
	TargetPresent    = "present"
	TargetEditedAway = "edited_away"
)

// Match is flagged content for the moderation review queue: the matched term
// plus the target's context. Type says what was flagged — a comment (CommentID/
// CommentBody set; VideoID is the video the comment is on) or a video
// (CommentID/CommentBody empty; VideoID/VideoTitle are the flagged video). The
// author is the comment's author or the video's owner respectively.
//
// MatchedText is the SNAPSHOT — the text as it read when the flag was raised —
// and MatchOffset/MatchLength locate the term inside it in runes (MatchOffset
// -1 when it could not be located). TargetStatus says whether the live target
// still contains the term. SnapshotBackfilled marks a row whose snapshot was
// reconstructed from the live text by migration 0132 rather than captured at
// flag time, so a moderator never mistakes one for the other. TermActive is
// false once the watched word itself was deleted — the match survives that now.
type Match struct {
	ID                 uuid.UUID
	Word               string
	Type               string
	CommentID          uuid.UUID
	CommentBody        string
	VideoID            uuid.UUID
	VideoTitle         string
	AuthorUsername     string
	CreatedAt          time.Time
	MatchedText        string
	MatchOffset        int32
	MatchLength        int32
	SnapshotBackfilled bool
	TermActive         bool
	TargetStatus       string
	Status             string
	ModeratorNote      string
	ResolvedAt         *time.Time
	ResolvedByUsername string
}

// ListMatches returns flagged comments and videos, newest match first, with the
// total. status filters the triage state ("open", "resolved", "dismissed"); an
// empty status (or StatusAll) returns every state. The caller clamps
// limit/offset. The total carries the SAME filter as the page, so the queue can
// never promise rows it will not serve.
func (s *Service) ListMatches(ctx context.Context, status string, limit, offset int32) ([]Match, int64, error) {
	var filter *string
	if status != "" && status != StatusAll {
		st := status
		filter = &st
	}
	rows, err := s.repo.ListWatchedWordMatches(ctx, sqlcgen.ListWatchedWordMatchesParams{
		Status:       filter,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountWatchedWordMatches(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Match, 0, len(rows))
	for _, r := range rows {
		m := Match{
			ID: r.ID, Word: r.Word, Type: MatchTargetVideo,
			VideoID: r.VideoID, AuthorUsername: r.AuthorUsername, CreatedAt: r.CreatedAt,
			MatchedText: r.MatchedText, MatchOffset: r.MatchOffset, MatchLength: r.MatchLength,
			SnapshotBackfilled: r.SnapshotBackfilled, TermActive: r.TermActive,
			TargetStatus: r.TargetStatus, Status: r.Status, ModeratorNote: r.ModeratorNote,
			ResolvedAt: pgconv.TimeOrNil(r.ResolvedAt),
		}
		if r.ResolvedByUsername != nil {
			m.ResolvedByUsername = *r.ResolvedByUsername
		}
		if r.VideoTitle != nil {
			m.VideoTitle = *r.VideoTitle
		}
		if r.CommentID.Valid {
			m.Type = MatchTargetComment
			m.CommentID = uuid.UUID(r.CommentID.Bytes)
			if r.CommentBody != nil {
				m.CommentBody = *r.CommentBody
			}
		}
		out = append(out, m)
	}
	return out, total, nil
}

// Resolve triages one match: status must be StatusResolved or StatusDismissed
// (validated by the caller), with the moderator's note stored on the match row.
// Idempotent exactly like the report queue's resolve — re-triaging an already
// triaged match overwrites the outcome and the note and still succeeds — so a
// retried request is never an error. An unknown id → ErrMatchNotFound.
func (s *Service) Resolve(ctx context.Context, moderatorID, matchID uuid.UUID, status, note string) error {
	n, err := s.repo.ResolveWatchedWordMatch(ctx, sqlcgen.ResolveWatchedWordMatchParams{
		ID:            matchID,
		Status:        status,
		ModeratorNote: note,
		ResolvedBy:    pgconv.UUID(moderatorID),
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrMatchNotFound
	}
	return nil
}
