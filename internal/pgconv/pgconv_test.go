package pgconv

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestUUID(t *testing.T) {
	id := uuid.New()
	got := UUID(id)
	if !got.Valid {
		t.Fatal("UUID must be non-null")
	}
	if uuid.UUID(got.Bytes) != id {
		t.Fatalf("bytes = %v, want %v", uuid.UUID(got.Bytes), id)
	}
	// The zero UUID is a value, not NULL: only UUIDPtr(nil) means NULL.
	if !UUID(uuid.Nil).Valid {
		t.Fatal("UUID(uuid.Nil) must still be non-null")
	}
}

func TestUUIDPtr(t *testing.T) {
	if got := UUIDPtr(nil); got.Valid {
		t.Fatal("UUIDPtr(nil) must be NULL")
	}
	id := uuid.New()
	got := UUIDPtr(&id)
	if !got.Valid || uuid.UUID(got.Bytes) != id {
		t.Fatalf("UUIDPtr(&id) = %+v, want valid %v", got, id)
	}
}

func TestTimeRoundTrip(t *testing.T) {
	now := time.Now().UTC()
	if got := Time(now); !got.Valid || !got.Time.Equal(now) {
		t.Fatalf("Time = %+v, want valid %v", got, now)
	}
	if got := TimePtr(nil); got.Valid {
		t.Fatal("TimePtr(nil) must be NULL")
	}
	if got := TimePtr(&now); !got.Valid || !got.Time.Equal(now) {
		t.Fatalf("TimePtr(&now) = %+v, want valid %v", got, now)
	}
	if got := TimeOrNil(TimePtr(nil)); got != nil {
		t.Fatalf("TimeOrNil(NULL) = %v, want nil", got)
	}
	got := TimeOrNil(Time(now))
	if got == nil || !got.Equal(now) {
		t.Fatalf("TimeOrNil(Time(now)) = %v, want %v", got, now)
	}
	// The returned pointer must not alias the caller's struct.
	*got = now.Add(time.Hour)
	if !Time(now).Time.Equal(now) {
		t.Fatal("TimeOrNil aliased its argument")
	}
}

func TestDeref(t *testing.T) {
	if got := Deref[string](nil); got != "" {
		t.Fatalf("Deref[string](nil) = %q, want empty", got)
	}
	s := "hello"
	if got := Deref(&s); got != "hello" {
		t.Fatalf("Deref(&s) = %q, want hello", got)
	}
	if got := Deref[int](nil); got != 0 {
		t.Fatalf("Deref[int](nil) = %d, want 0", got)
	}
	if got := DerefOr[string](nil, "fallback"); got != "fallback" {
		t.Fatalf("DerefOr(nil) = %q, want fallback", got)
	}
	if got := DerefOr(&s, "fallback"); got != "hello" {
		t.Fatalf("DerefOr(&s) = %q, want hello", got)
	}
}

func TestTrimPtr(t *testing.T) {
	// nil stays nil so a COALESCE update skips the column entirely.
	if got := TrimPtr(nil); got != nil {
		t.Fatalf("TrimPtr(nil) = %v, want nil", got)
	}
	s := "  padded  "
	got := TrimPtr(&s)
	if got == nil || *got != "padded" {
		t.Fatalf("TrimPtr = %v, want \"padded\"", got)
	}
	if s != "  padded  " {
		t.Fatalf("TrimPtr mutated its argument: %q", s)
	}
	// A string that trims to empty is still a present (non-nil) value.
	blank := "   "
	if got := TrimPtr(&blank); got == nil || *got != "" {
		t.Fatalf("TrimPtr(blank) = %v, want pointer to empty string", got)
	}
}

func TestSQLState(t *testing.T) {
	if got := SQLState(nil); got != "" {
		t.Fatalf("SQLState(nil) = %q, want empty", got)
	}
	if got := SQLState(errors.New("plain")); got != "" {
		t.Fatalf("SQLState(plain) = %q, want empty", got)
	}
	pgErr := &pgconn.PgError{Code: SQLStateForeignKeyViolation}
	if got := SQLState(pgErr); got != SQLStateForeignKeyViolation {
		t.Fatalf("SQLState = %q, want %q", got, SQLStateForeignKeyViolation)
	}
	// Wrapped errors must still be classified — services wrap before returning.
	if got := SQLState(fmt.Errorf("insert: %w", pgErr)); got != SQLStateForeignKeyViolation {
		t.Fatalf("SQLState(wrapped) = %q, want %q", got, SQLStateForeignKeyViolation)
	}
}

func TestIsUniqueViolation(t *testing.T) {
	if IsUniqueViolation(nil) || IsUniqueViolation(errors.New("plain")) {
		t.Fatal("non-pg errors must not be unique violations")
	}
	if IsUniqueViolation(&pgconn.PgError{Code: SQLStateForeignKeyViolation}) {
		t.Fatal("23503 must not read as a unique violation")
	}
	unique := &pgconn.PgError{Code: SQLStateUniqueViolation}
	if !IsUniqueViolation(unique) {
		t.Fatal("23505 must read as a unique violation")
	}
	if !IsUniqueViolation(fmt.Errorf("insert: %w", unique)) {
		t.Fatal("wrapped 23505 must read as a unique violation")
	}
}
