// Package pgconv holds the handful of value conversions every service needs to
// sit between Go types and the pgtype values sqlc hands back — plus the two
// SQLSTATE predicates the write paths use to recognise a lost constraint race.
//
// Each of these was previously redefined per-package (six copies of
// isUniqueViolation, four of pgUUID, four of trimPtr, four differently-named
// spellings of the same *string deref, and a time-pointer family under five
// names), which is how the codebase ended up with two isUniqueViolation
// implementations that disagreed about whether to depend on pgconn.
//
// This package wraps the values sqlc produces; it does not replace or wrap the
// generated code in internal/store/sqlcgen.
package pgconv

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// UUID wraps a uuid.UUID as a non-null pgtype.UUID for a query parameter. The
// zero UUID is a legitimate value here, not NULL — callers that mean "no id"
// pass a nil pointer to UUIDPtr instead.
func UUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// UUIDPtr wraps an optional uuid.UUID, rendering nil as SQL NULL.
func UUIDPtr(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

// Time wraps a time.Time as a non-null pgtype.Timestamptz.
func Time(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// TimePtr wraps an optional time.Time, rendering nil as SQL NULL.
func TimePtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// TimeOrNil is the read direction: a nullable timestamptz as *time.Time, nil
// when NULL. It copies the time out of the struct so the caller's pointer does
// not alias the row.
func TimeOrNil(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time
	return &t
}

// Deref returns the pointee, or T's zero value when p is nil.
func Deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// DerefOr returns the pointee, or def when p is nil.
func DerefOr[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

// TrimPtr trims a non-nil string pointer's value, leaving nil untouched so a
// COALESCE update skips the column.
func TrimPtr(p *string) *string {
	if p == nil {
		return nil
	}
	t := strings.TrimSpace(*p)
	return &t
}

// SQLState returns the SQLSTATE code of a PostgreSQL error, "" otherwise.
//
// It matches on the SQLState() method rather than *pgconn.PgError so callers do
// not take a pgconn dependency to classify an error the driver already
// describes.
func SQLState(err error) string {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState()
	}
	return ""
}

// SQLStateUniqueViolation is Postgres' unique_violation. Write paths that rely
// on a unique index as the authority ("one active campaign", "one follow per
// pair") have to recognise losing that race.
const SQLStateUniqueViolation = "23505"

// SQLStateForeignKeyViolation is Postgres' foreign_key_violation — e.g. muting
// or reporting a row that does not exist.
const SQLStateForeignKeyViolation = "23503"

// IsUniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation (SQLSTATE 23505).
func IsUniqueViolation(err error) bool {
	return SQLState(err) == SQLStateUniqueViolation
}
