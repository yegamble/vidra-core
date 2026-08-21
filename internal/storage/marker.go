package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// OwnerMarkerKey is the object that says which Vidra install owns this store.
// Its body is that install's instance-identity UUID as plain text, written once
// and never rewritten in place.
//
// It exists because media garbage collection deletes every object it cannot
// attribute to a database row, and "the database does not reference it" is only
// evidence of an orphan if the database and the store belong to the same
// install. Point a fresh Vidra at a colleague's bucket, at a bucket that still
// holds the previous install's media, or at the DESTINATION of a half-finished
// migration, and every object in it looks like an orphan.
//
// The key sits outside every swept prefix on purpose (see
// internal/mediagc.sweptPrefixes) — the marker must not be able to collect
// itself — and it passes both backends' key rules: a leading dot is not
// special to either (Local resolves it as an ordinary hidden directory, S3 has
// no notion of one), only empty/absolute/NUL/".."-bearing keys are rejected.
const OwnerMarkerKey = ".vidra/owner"

// ReadOwnerMarker returns the identity recorded in the store's ownership
// marker. found is false when there is no marker at all, which is a normal
// answer (a fresh bucket, or one written by a Vidra older than this marker) and
// not an error. Any other failure — credentials, network, a truncated read — is
// returned as an error so the caller can fail SAFE rather than read "absent"
// into a blip.
func ReadOwnerMarker(ctx context.Context, b Backend) (identity string, found bool, err error) {
	rc, err := b.Open(ctx, OwnerMarkerKey)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("storage: read owner marker: %w", err)
	}
	defer func() { _ = rc.Close() }()
	// The marker is a UUID; anything appreciably longer is not one, and reading
	// an unbounded object into memory because someone put a video at that key is
	// not a thing a boot-time probe should do.
	body, err := io.ReadAll(io.LimitReader(rc, 4096))
	if err != nil {
		return "", false, fmt.Errorf("storage: read owner marker: %w", err)
	}
	return strings.TrimSpace(string(body)), true, nil
}

// WriteOwnerMarker stamps identity onto the store as its ownership marker. It
// overwrites whatever was there, so callers must have decided that claiming the
// store is correct BEFORE calling — this is the write half of an adoption, not
// a check.
func WriteOwnerMarker(ctx context.Context, b Backend, identity string) error {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return errors.New("storage: write owner marker: identity is empty")
	}
	r := strings.NewReader(identity)
	if _, err := PutSized(ctx, b, OwnerMarkerKey, r, int64(r.Len())); err != nil {
		return fmt.Errorf("storage: write owner marker: %w", err)
	}
	return nil
}
