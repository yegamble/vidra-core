// Package blobverify answers one question about a deployment: does every object
// the database references still exist in the object store, and — when asked —
// are its bytes still the bytes that were written (phase-2 storage, work
// item 7).
//
// It is the other half of media garbage collection. The GC sweep enumerates the
// STORE and looks for objects no row references; this enumerates the DATABASE
// and looks for rows no object backs. The two share one derivation of the key
// grammar on purpose (mediagc.ListReferences): a second, independently written
// answer to "what does the database reference?" would drift, and whichever copy
// drifted would either delete live media or declare a healthy store broken.
//
// WHY IT EXISTS, CONCRETELY. A pg_dump is taken at time T; the bucket is
// whatever it is at T+n. Restore that dump and the two are no longer a matched
// pair — a video uploaded after the dump has an object and no row (the GC's
// problem, and the GC's rails are what keep it from deleting one mid-restore),
// while a video deleted after the dump has a row and no object (this package's
// problem, and it is the one a viewer notices). Nothing detects the second case
// on its own: the API 404s one video at a time, forever, and an operator finds
// out from a report.
//
// IT IS READ-ONLY, ALWAYS. It calls Exists, Open and ListKeys and nothing else.
// There is no repair mode and no --fix: every plausible repair (delete the row,
// re-derive the object) destroys information, and the only party who can say
// which of the two stores is the stale one is the operator who knows what
// happened.
package blobverify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/vidra/vidra-core/internal/mediagc"
	"github.com/vidra/vidra-core/internal/mediahash"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/workerpool"
)

// SampleLimit is how many keys of each problem class the report carries. The
// counts are always complete; the key lists are a SAMPLE, because a store that
// lost a prefix produces tens of thousands of them and an operator needs the
// first few plus the number, not a log file they cannot page through.
const SampleLimit = 20

// DefaultConcurrency is how many objects are checked at once. Deliberately
// modest: this runs during a restore, against the same object store the
// deployment is about to serve from, and an existence check is a network
// round-trip whose cost is latency rather than bandwidth. Eight overlaps enough
// of that latency to matter without turning a consistency check into a load
// test of the bucket.
const DefaultConcurrency = 8

// Repository is the data access the check needs. It is mediagc's reference set
// — the same queries the sweep matches against — plus the storage_key columns
// the sweep deliberately never enumerates (avatars, banners, export archives,
// DM attachments, the instance's own images: objects other lifecycles own) and
// the recorded content digests. *sqlcgen.Queries satisfies it directly; tests
// substitute an in-memory fake.
type Repository interface {
	mediagc.Repository

	ListAllUserImageKeys(ctx context.Context) ([]string, error)
	ListAllChannelImageKeys(ctx context.Context) ([]string, error)
	ListAllInstanceImageKeys(ctx context.Context) ([]string, error)
	ListAllAccountExportKeys(ctx context.Context) ([]string, error)
	ListAllMessageAttachmentKeys(ctx context.Context) ([]string, error)
	ListAllVideoFileHashes(ctx context.Context) ([]sqlcgen.ListAllVideoFileHashesRow, error)
}

// Options tune one run.
type Options struct {
	// Hash re-downloads every object whose row carries a real digest and
	// compares. It is the expensive mode — it reads the whole library — and it
	// is the only mode that can detect corruption rather than absence.
	Hash bool
	// Deep enumerates each HLS tree through the backend's ObjectLister instead
	// of trusting that a present master manifest implies a present ladder.
	Deep bool
	// Concurrency is how many objects are inspected at once; 0 means
	// DefaultConcurrency. Clamped to [1, DefaultConcurrency].
	Concurrency int
}

// Report is what one run found. Every count is complete; every key list is
// capped at SampleLimit.
//
// The classes are deliberately not collapsed into one "bad" number. "The object
// is gone" and "the object is there and its bytes changed" have different
// causes and different responses, and "the backfill already recorded this row
// as dangling" is not a new finding at all.
type Report struct {
	// Hash and Deep echo the modes the run actually used, so a report pasted
	// into an issue says what was and was not checked.
	Hash bool `json:"hash"`
	Deep bool `json:"deep"`

	// Checked is how many distinct object keys were looked for.
	Checked int `json:"checked"`
	// Present is how many of them the store has.
	Present int `json:"present"`
	// Missing is the finding that matters most: a row references an object the
	// store does not have, and nothing had recorded that before this run.
	Missing int `json:"missing"`
	// Mismatched is corruption: the object is there and its bytes no longer
	// hash to what was recorded when they were written. Only ever non-zero with
	// Hash.
	Mismatched int `json:"mismatched"`
	// Verified is how many objects were re-read and matched their digest.
	Verified int `json:"verified"`
	// Unhashed is how many present objects carry no digest to compare against
	// (rows older than the hash-on-Put paths whose backfill has not reached
	// them, and every class of object that has no video_files row at all).
	Unhashed int `json:"unhashed"`
	// KnownMissing is rows whose sha256 is the backfill's 'missing' sentinel and
	// whose object is indeed absent. A pre-existing, already-recorded dangling
	// reference — reported every time, but not counted as a new inconsistency.
	KnownMissing int `json:"known_missing"`
	// StaleSentinel is rows carrying the sentinel whose object IS there. The
	// object came back (a restored bucket, a re-upload); the row's digest is now
	// wrong and the backfill will never revisit it, because the sentinel is a
	// terminal state.
	StaleSentinel int `json:"stale_sentinel"`
	// Incomplete is HLS trees whose master manifest is present but whose
	// directory holds nothing else. Only ever non-zero with Deep.
	Incomplete int `json:"incomplete"`
	// Skipped is references that could not be turned into a question: a
	// streaming playlist whose transcode never produced a master.
	Skipped int `json:"skipped"`
	// Errors is objects the store could not answer for — a timeout, a 5xx, a
	// permission problem. Not "missing": an unanswerable question about an
	// object must never be reported as a lost one.
	Errors int `json:"errors"`

	// DeepListed / DeepPlaylists are what the deep pass enumerated: objects
	// found under the trees, and how many trees it walked.
	DeepListed    int `json:"deep_listed"`
	DeepPlaylists int `json:"deep_playlists"`
	// DeepUnsupported means --deep was asked for and the backend cannot list
	// objects, so the trees were checked by their master manifest only.
	DeepUnsupported bool `json:"deep_unsupported"`

	// Sample keys, capped at SampleLimit each.
	MissingKeys        []string `json:"missing_keys,omitempty"`
	MismatchedKeys     []string `json:"mismatched_keys,omitempty"`
	KnownMissingKeys   []string `json:"known_missing_keys,omitempty"`
	StaleSentinelKeys  []string `json:"stale_sentinel_keys,omitempty"`
	IncompletePrefixes []string `json:"incomplete_prefixes,omitempty"`
	ErrorKeys          []string `json:"error_keys,omitempty"`
}

// Problems is the count that decides the exit code: findings this run made that
// nobody had recorded before. KnownMissing is deliberately not in it — those
// rows were already dangling when the dump was taken, they are reported in full
// every time, and failing on them would make a post-restore check that never
// goes green, which is a check operators learn to ignore.
func (r Report) Problems() int {
	return r.Missing + r.Mismatched + r.Incomplete + r.Errors
}

// Consistent reports whether the database and the store agree.
func (r Report) Consistent() bool { return r.Problems() == 0 }

// Verify runs the check. The error return is reserved for a run that could not
// be made at all — the database would not answer, or the context was cancelled.
// Everything the store said, including everything it said badly, is in the
// Report: a per-object failure is a finding, not a reason to abandon the other
// forty thousand.
func Verify(ctx context.Context, repo Repository, blobs storage.Backend, opt Options) (Report, error) {
	if repo == nil {
		return Report{}, errors.New("blobverify: no database")
	}
	if blobs == nil {
		return Report{}, errors.New("blobverify: no storage backend")
	}
	rep := Report{Hash: opt.Hash, Deep: opt.Deep}

	targets, playlists, skipped, err := enumerate(ctx, repo)
	if err != nil {
		return Report{}, err
	}
	rep.Skipped = skipped
	rep.Checked = len(targets)

	workers := opt.Concurrency
	if workers <= 0 {
		workers = DefaultConcurrency
	}
	if workers > DefaultConcurrency {
		workers = DefaultConcurrency
	}
	workers = workerpool.Clamp(int64(workers))

	results := make([]outcome, len(targets))
	workerpool.Run(workers, len(targets), func(i int) {
		results[i] = inspect(ctx, blobs, targets[i], opt.Hash)
	})
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	for _, res := range results {
		rep.fold(res)
	}

	if opt.Deep {
		deepWalk(ctx, blobs, playlists, &rep)
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
	}

	sort.Strings(rep.MissingKeys)
	sort.Strings(rep.MismatchedKeys)
	sort.Strings(rep.KnownMissingKeys)
	sort.Strings(rep.StaleSentinelKeys)
	sort.Strings(rep.IncompletePrefixes)
	sort.Strings(rep.ErrorKeys)
	return rep, nil
}

// target is one object the database says must exist.
type target struct {
	key string
	// sha is the digest recorded for this object: "" when nothing recorded one
	// (every class but video_files, plus video_files rows the backfill has not
	// reached), mediahash.SentinelMissing when the backfill already found the
	// object gone, or 64 hex characters.
	sha string
}

type class int

const (
	classPresent class = iota
	classMissing
	classKnownMissing
	classStaleSentinel
	classMismatch
	classVerified
	classError
)

type outcome struct {
	key string
	cls class
}

// enumerate turns the database into the list of questions to ask the store.
// Duplicate keys collapse: one object referenced by two rows is one Exists
// call, and reporting it twice would double a count an operator reads as
// "objects".
func enumerate(ctx context.Context, repo Repository) (targets []target, playlists []mediagc.PlaylistRef, skipped int, err error) {
	refs, err := mediagc.ListReferences(ctx, repo)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("blobverify: enumerate references: %w", err)
	}

	// The recorded digests, by key. Attached to the reference set rather than
	// enumerated separately so video_files contributes its keys through exactly
	// one derivation (mediagc's) and its hashes through another.
	hashRows, err := repo.ListAllVideoFileHashes(ctx)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("blobverify: read content hashes: %w", err)
	}
	hashes := make(map[string]string, len(hashRows))
	for _, row := range hashRows {
		key := strings.TrimSpace(row.StorageKey)
		if key == "" {
			continue
		}
		// Two rows on one key is pathological but not impossible; prefer a real
		// digest over the sentinel or the empty state, so a shared object is
		// verified rather than excused.
		if prev, seen := hashes[key]; seen && isDigest(prev) {
			continue
		}
		hashes[key] = strings.TrimSpace(row.Sha256)
	}

	seen := make(map[string]int, len(refs.Keys))
	add := func(key, sha string) {
		key = strings.TrimSpace(key)
		if key == "" {
			return
		}
		if idx, ok := seen[key]; ok {
			if targets[idx].sha == "" {
				targets[idx].sha = sha
			}
			return
		}
		seen[key] = len(targets)
		targets = append(targets, target{key: key, sha: sha})
	}

	for _, key := range refs.Keys {
		add(key, hashes[key])
	}

	// The storage_key columns the GC sweep never enumerates, because their
	// prefixes are owned by other lifecycles. A dangling reference in any of
	// them is exactly as broken as one in video_files.
	extra := []func(context.Context) ([]string, error){
		repo.ListAllUserImageKeys,
		repo.ListAllChannelImageKeys,
		repo.ListAllInstanceImageKeys,
		repo.ListAllAccountExportKeys,
		repo.ListAllMessageAttachmentKeys,
	}
	for _, list := range extra {
		keys, lerr := list(ctx)
		if lerr != nil {
			return nil, nil, 0, fmt.Errorf("blobverify: enumerate references: %w", lerr)
		}
		for _, key := range keys {
			add(key, "")
		}
	}

	for _, pl := range refs.Playlists {
		if pl.MasterKey == "" {
			// A transcode that dead-lettered. There is no object to ask about,
			// and treating "never produced" as "lost" would report every failed
			// job as data loss.
			skipped++
			continue
		}
		playlists = append(playlists, pl)
		add(pl.MasterKey, "")
	}

	sort.Slice(targets, func(i, j int) bool { return targets[i].key < targets[j].key })
	return targets, playlists, skipped, nil
}

// inspect asks the store about one object.
func inspect(ctx context.Context, blobs storage.Backend, t target, wantHash bool) outcome {
	if err := ctx.Err(); err != nil {
		return outcome{key: t.key, cls: classError}
	}
	ok, err := blobs.Exists(ctx, t.key)
	switch {
	case err != nil:
		return outcome{key: t.key, cls: classError}
	case !ok && t.sha == mediahash.SentinelMissing:
		return outcome{key: t.key, cls: classKnownMissing}
	case !ok:
		return outcome{key: t.key, cls: classMissing}
	case t.sha == mediahash.SentinelMissing:
		return outcome{key: t.key, cls: classStaleSentinel}
	}
	if !wantHash || !isDigest(t.sha) {
		return outcome{key: t.key, cls: classPresent}
	}
	sum, err := hashObject(ctx, blobs, t.key)
	switch {
	case err != nil:
		return outcome{key: t.key, cls: classError}
	case sum != t.sha:
		return outcome{key: t.key, cls: classMismatch}
	default:
		return outcome{key: t.key, cls: classVerified}
	}
}

// hashObject streams the object through SHA-256. It never buffers: the objects
// here are whole video originals, and the point of hashing on Put in the first
// place was that nothing has to hold one in memory.
func hashObject(ctx context.Context, blobs storage.Backend, key string) (string, error) {
	rc, err := blobs.Open(ctx, key)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// deepWalk enumerates each HLS tree instead of inferring it from its master.
//
// It exists because the master manifest is the ONLY object in a tree that has a
// database row behind it. Everything a viewer actually downloads — the rendition
// playlists and every segment under them — is unrecorded, so a partial restore
// that brought back one small text file per video and none of the segments
// passes the fast pass with a clean bill of health and plays nothing.
func deepWalk(ctx context.Context, blobs storage.Backend, playlists []mediagc.PlaylistRef, rep *Report) {
	lister, ok := blobs.(storage.ObjectLister)
	if !ok {
		rep.DeepUnsupported = true
		return
	}
	for _, pl := range playlists {
		if err := ctx.Err(); err != nil {
			return
		}
		dir := path.Dir(pl.MasterKey)
		if dir == "." || dir == "/" || dir == "" {
			// A master key with no directory part is not a shape this layout
			// produces; fall back to the video's own tree prefix rather than
			// listing the bucket root.
			dir = pl.Prefix
		}
		keys, err := lister.ListKeys(ctx, dir)
		if err != nil {
			rep.Errors++
			rep.sample(&rep.ErrorKeys, dir)
			continue
		}
		rep.DeepPlaylists++
		rep.DeepListed += len(keys)
		others := 0
		for _, k := range keys {
			if k != pl.MasterKey {
				others++
			}
		}
		if others == 0 {
			rep.Incomplete++
			rep.sample(&rep.IncompletePrefixes, dir)
		}
	}
}

func (r *Report) fold(res outcome) {
	switch res.cls {
	case classPresent:
		r.Present++
		r.Unhashed++
	case classVerified:
		r.Present++
		r.Verified++
	case classMismatch:
		r.Present++
		r.Mismatched++
		r.sample(&r.MismatchedKeys, res.key)
	case classStaleSentinel:
		r.Present++
		r.Unhashed++
		r.StaleSentinel++
		r.sample(&r.StaleSentinelKeys, res.key)
	case classMissing:
		r.Missing++
		r.sample(&r.MissingKeys, res.key)
	case classKnownMissing:
		r.KnownMissing++
		r.sample(&r.KnownMissingKeys, res.key)
	case classError:
		r.Errors++
		r.sample(&r.ErrorKeys, res.key)
	}
}

func (r *Report) sample(dst *[]string, key string) {
	if len(*dst) >= SampleLimit {
		return
	}
	*dst = append(*dst, key)
}

// isDigest reports whether a recorded sha256 value is a real digest, as opposed
// to the empty (not-computed) state or the missing sentinel.
func isDigest(v string) bool {
	if len(v) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil && strings.ToLower(v) == v
}

// Text renders the report as the operator-facing summary. It is here rather
// than in the command so the wording is testable and so a future admin surface
// prints the same sentences a restore does.
func (r Report) Text() string {
	var b strings.Builder
	mode := "existence only"
	switch {
	case r.Hash && r.Deep:
		mode = "existence + content hashes + HLS trees"
	case r.Hash:
		mode = "existence + content hashes"
	case r.Deep:
		mode = "existence + HLS trees"
	}
	fmt.Fprintf(&b, "checked %d referenced object(s) — %s\n", r.Checked, mode)
	fmt.Fprintf(&b, "  present:        %d\n", r.Present)
	if r.Hash {
		fmt.Fprintf(&b, "  hash verified:  %d\n", r.Verified)
		fmt.Fprintf(&b, "  no digest yet:  %d\n", r.Unhashed)
	}
	fmt.Fprintf(&b, "  MISSING:        %d\n", r.Missing)
	fmt.Fprintf(&b, "  MISMATCHED:     %d\n", r.Mismatched)
	fmt.Fprintf(&b, "  known missing:  %d (rows the hash backfill already recorded as dangling)\n", r.KnownMissing)
	if r.StaleSentinel > 0 {
		fmt.Fprintf(&b, "  sentinel stale: %d (recorded as missing, but the object is there now)\n", r.StaleSentinel)
	}
	if r.Deep {
		if r.DeepUnsupported {
			b.WriteString("  HLS trees:      not walked — this storage backend cannot list objects\n")
		} else {
			fmt.Fprintf(&b, "  HLS trees:      %d walked, %d object(s) listed, %d with a master and nothing else\n",
				r.DeepPlaylists, r.DeepListed, r.Incomplete)
		}
	}
	fmt.Fprintf(&b, "  unreadable:     %d\n", r.Errors)
	fmt.Fprintf(&b, "  skipped:        %d (streaming playlists whose transcode produced no master)\n", r.Skipped)

	writeSamples(&b, "missing", r.Missing, r.MissingKeys)
	writeSamples(&b, "mismatched (CORRUPT)", r.Mismatched, r.MismatchedKeys)
	writeSamples(&b, "known missing", r.KnownMissing, r.KnownMissingKeys)
	writeSamples(&b, "sentinel stale", r.StaleSentinel, r.StaleSentinelKeys)
	writeSamples(&b, "HLS trees with no objects besides the master", r.Incomplete, r.IncompletePrefixes)
	writeSamples(&b, "unreadable", r.Errors, r.ErrorKeys)

	switch {
	case r.Consistent() && r.KnownMissing == 0:
		b.WriteString("\nconsistent: every object the database references is in the store.\n")
	case r.Consistent():
		b.WriteString("\nconsistent: every object the database references is in the store, apart from the known-missing rows above — those were already recorded as dangling before this run.\n")
	default:
		b.WriteString("\nINCONSISTENT: the database and the object store do not agree. Nothing was changed — this check only reads.\n")
	}
	return b.String()
}

func writeSamples(b *strings.Builder, label string, total int, keys []string) {
	if total == 0 || len(keys) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s (%d), first %d:\n", label, total, len(keys))
	for _, k := range keys {
		fmt.Fprintf(b, "  %s\n", k)
	}
	if total > len(keys) {
		fmt.Fprintf(b, "  … and %d more\n", total-len(keys))
	}
}
