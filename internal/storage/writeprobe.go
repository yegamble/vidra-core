package storage

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
)

// WriteProbePrefix is where a write probe's scratch object lives.
//
// It exists because nothing else in this package asks the one question that
// matters before an instance is asked to store anything: CAN this credential
// write? Every other probe here is a read. EnsureBucket/BucketExists is a
// HeadBucket, ReadOwnerMarker is a GET, IsEmpty is a list — and an S3 credential
// scoped to reads passes all three. A real migration ran for three minutes on
// exactly such a key and then failed 1,321 avatar uploads with
// `s3: put "avatars/users/…": not entitled`, one warning per image, which is
// where the operator found out.
//
// The prefix sits outside every swept prefix for the same reason
// OwnerMarkerKey does (see internal/mediagc.sweptPrefixes): media garbage
// collection deletes objects it cannot attribute to a database row, and a
// scratch object is by definition unattributable. It shares the marker's
// `.vidra/` directory — both are Vidra's own bookkeeping rather than media — but
// it is a DIFFERENT, deeper path and every probe key under it is unique, so no
// probe can read, write, overwrite or delete the marker. That separation is the
// point: writing the marker is an ADOPTION decision that belongs to an explicit
// admin action, never to a diagnostic (see internal/doctor/host.go), while
// writing a probe object claims nothing and is removed again immediately.
//
// The leading dot passes both backends' key rules, exactly as the marker's
// does: Local resolves it as an ordinary hidden directory and S3 has no notion
// of one.
const WriteProbePrefix = ".vidra/write-probe/"

// writeProbeBody is what a probe object contains. It is prose rather than random
// bytes because the one person who will ever read it is an operator who found it
// in a bucket listing after a probe was killed between the PUT and the DELETE,
// and their question is "what is this, and can I remove it?".
const writeProbeBody = "vidra storage write probe — scratch object, safe to delete.\n"

// WriteProbe is what one probe learned. The zero value means nothing was
// attempted.
type WriteProbe struct {
	// Key is the object the probe used, whether or not the write succeeded. It is
	// reported so a leaked object can be named to the operator rather than left
	// for them to find.
	Key string
	// Wrote reports that the store accepted the object. It is the whole verdict:
	// true means this credential can PutObject into this bucket.
	Wrote bool
	// CleanupErr is why the scratch object could not be removed again, and it is
	// deliberately NOT folded into ProbeWrite's error return. A store that took
	// the write and refused the delete has answered the question that was asked —
	// yes, it can write — and turning that into a failure would report an
	// unwritable destination to an operator whose destination is writable. It is
	// its own condition (on Backblaze B2, deleteFiles is granted separately from
	// writeFiles) and it deserves its own sentence.
	CleanupErr error
}

// Leaked reports whether a scratch object was written and is still there.
func (p WriteProbe) Leaked() bool { return p.Wrote && p.CleanupErr != nil }

// ProbeWrite proves this backend will accept a write from these credentials, by
// storing a tiny object at a key nothing else uses and removing it again.
//
// The returned error is non-nil ONLY when the store refused the write; a failed
// cleanup is reported in the result. Callers deciding whether a destination is
// usable should branch on the error, and report result.CleanupErr separately.
//
// It works through Backend, so it covers every backend rather than only S3 — the
// local backend answers the same question about a media root the process cannot
// write to (a read-only mount, a volume owned by another uid). It deliberately
// does NOT check the bucket exists or that the object reads back: BucketExists
// already covers the first as the cheapest authenticated round trip there is,
// and the second is a different permission (s3:GetObject) that the ownership
// marker read already exercises. This call is about s3:PutObject and nothing
// else.
func ProbeWrite(ctx context.Context, b Backend) (WriteProbe, error) {
	res := WriteProbe{Key: newWriteProbeKey()}
	body := strings.NewReader(writeProbeBody)
	if _, err := PutSized(ctx, b, res.Key, body, int64(body.Len())); err != nil {
		// Clean up on the failure path too. A PUT can fail AFTER the object has
		// landed — a multipart completion that times out, a proxy that drops the
		// response — and this is the path a misconfigured deployment takes every
		// time, so it is the last one that should be allowed to litter. The delete's
		// own error is discarded: there is already a failure to report, and a second
		// one about tidying up would bury it.
		_ = b.Delete(ctx, res.Key)
		return res, fmt.Errorf("storage: write probe: put %q: %w", res.Key, err)
	}
	res.Wrote = true
	if err := b.Delete(ctx, res.Key); err != nil {
		res.CleanupErr = fmt.Errorf("storage: write probe: delete %q: %w", res.Key, err)
	}
	return res, nil
}

// newWriteProbeKey returns a key no other probe will use. Uniqueness is not
// tidiness: doctor, a migration preflight and the api can probe the same bucket
// at the same moment, and on a shared key one probe's DELETE removes the object
// another has just written — which reads as a store that lost a write.
//
// crypto/rand.Text is the source because it cannot fail and needs no error path
// in a function whose whole job is to be cheap; its base32 alphabet is safe in
// an object key on every backend.
func newWriteProbeKey() string {
	return WriteProbePrefix + rand.Text()
}
