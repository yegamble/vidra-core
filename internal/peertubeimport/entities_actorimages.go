package peertubeimport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/profileimage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// This file carries account and channel AVATARS and BANNERS (the source's
// actorImage table) onto Vidra's user_images / channel_images.
//
// It is a PASS OF ITS OWN with its own ledger kinds, for the same reason the
// per-video families are (see entities_pervideo.go): importOneUser and
// importOneChannel never run again for an entity with a terminal ledger row, so
// folding avatars into them would reach only accounts imported after this
// shipped and leave every already-migrated instance faceless. Its own pass
// backfills onto the users and channels that are already there.
//
// Actor images are one of the three families PeerTube keeps OFF its object
// store, so they are fetched over HTTP from the source's public origin rather
// than read through srcMedia. The whole of that reasoning — including the
// /static/ SPA-fallback trap the content-type gate exists for — is in
// lazystatic.go, which owns the fetcher this pass drives.

// ── deciding what to do with a slot that is already filled ──
//
// The operator runs this import on a schedule against a source that keeps
// changing right up to cutover, so the pass is asked the same question the
// taxonomy pass is (see entities_taxonomy.go's decideTaxonomy): is this thing
// still the import's to update?
//
// "Write it once and never again" was the old answer, and it is why an instance
// migrated with the multi-variant bug cannot heal: profileimage deliberately
// never overwrites, the ledger rows are terminal, so 137 thumbnail avatars would
// have stayed thumbnails forever short of hand-editing the database. "Write it
// every time" is not an answer either — it silently replaces an avatar a person
// uploaded here, every night.
//
// The third answer needs memory of what the import itself put in the slot. That
// is applied_value (migration 0113), recorded here as a fingerprint of the
// stored object, and it makes ownership a matter of evidence rather than of
// policy: an image that still matches the fingerprint is the import's to update,
// anything else is somebody's and is left alone AND reported.

// importMode says how far a run may go when a slot on this instance is already
// filled with something the import did not write. It is the same question, and
// the same answer, for every media slot the import owns — actor images here,
// video posters and storyboards in entities_videoimages.go — so it is named for
// the run and not for one family.
type importMode int

const (
	// modeGapFill is the default and the conservative one: fill empty slots,
	// update an image the import can prove it wrote, never touch one a person
	// put there.
	modeGapFill importMode = iota
	// modeSourceAuthoritative makes the SOURCE the truth for these slots — for
	// the operator who is syncing a live PeerTube onto an instance they also
	// edit and has said which side wins.
	//
	// It changes exactly one thing: the two outcomes that would leave a divergent
	// slot alone become writes. Everything else is identical, and in particular
	// "the slot already holds what the import put there" still writes NOTHING, so
	// the expensive half (a fetch from the live source, a PUT into object storage)
	// is spent only where the two sides actually differ.
	modeSourceAuthoritative
)

// actorImageSlot is the DESTINATION's side of the decision for one avatar or
// banner slot. Every field is evidence already on this instance; nothing here
// requires touching the source, which is what lets the whole decision be made
// before a single HTTP request leaves the machine.
type actorImageSlot struct {
	// present and current describe the image in the slot right now, current being
	// the same fingerprint shape the import records for its own writes.
	present bool
	current string
	// carried is the fingerprint the import recorded the last time it wrote into
	// this slot. Empty means it has no such memory — either it never wrote here,
	// or it wrote before the memory existed (which is what wroteBefore is for).
	carried string
	// wroteBefore says the ledger holds a COMPLETED import write onto this slot.
	// It is the only evidence available for images carried by releases that
	// recorded no fingerprint, and it is what lets an already-migrated instance
	// heal itself exactly once.
	wroteBefore bool
	// untouched says nothing has written the slot since that ledger row landed.
	// The pass records its ledger row AFTER the image write, so a slot whose image
	// is NEWER than the row was filled by somebody else — the guard that stops the
	// heal-once path from trampling an avatar a person uploaded in the meantime.
	untouched bool
	// carriedThisFile says the ledger already records carrying THIS source file
	// into this slot.
	//
	// It is deliberately not the whole answer, which is the subtlety the old
	// per-file check got wrong: on the instance this fixes, the import carried
	// FOUR variants of every avatar and whichever one won the race is what is in
	// the slot — so every variant, the largest included, carries a 'done' row
	// while the slot holds something else entirely.
	carriedThisFile bool
}

// actorImageAction is what a run decided to do with one slot.
type actorImageAction int

const (
	// actorImageWrite — the slot is empty and filling it is what an import is for.
	actorImageWrite actorImageAction = iota
	// actorImageReplace — the slot holds an image the import wrote, and the source
	// now offers a different (or better) one.
	actorImageReplace
	// actorImageUpToDate — the slot already holds exactly this carry. No request,
	// no write, no cost: the same "a delta of zero writes nothing" discipline the
	// view-count pass keeps.
	actorImageUpToDate
	// actorImageOperatorOwned — the slot holds an image the import cannot claim.
	// Left exactly as it is, and reported.
	actorImageOperatorOwned
	// actorImageCleared — the import filled this slot and it is empty again.
	// Somebody removed the picture, which is a decision and not a gap to refill.
	actorImageCleared
)

// decideActorImage is the whole of the replace-versus-clobber judgement: a pure
// function of what this instance holds, what the import remembers writing, and
// which side the operator said wins. The source's side of it is one bit —
// carriedThisFile — because by the time this is asked the source has already
// been reduced to ONE best variant per slot.
func decideActorImage(s actorImageSlot, mode importMode) actorImageAction {
	switch {
	case !s.present:
		if s.carried != "" || s.wroteBefore || s.carriedThisFile {
			if mode == modeSourceAuthoritative {
				return actorImageWrite
			}
			return actorImageCleared
		}
		return actorImageWrite

	case s.carried != "" && s.current == s.carried:
		// The slot still holds precisely what the import put there.
		if s.carriedThisFile {
			return actorImageUpToDate
		}
		return actorImageReplace // ...but the source has moved to another variant

	case s.carried == "" && s.wroteBefore && s.untouched:
		// Carried by a release that recorded no fingerprint, and untouched since.
		// This is the one-time self-heal: it is the only way the 137 thumbnails an
		// earlier import wrote can be replaced by the full-size originals that were
		// in the source all along. It costs one fetch per slot, once — after which
		// the write records a fingerprint and the branch above takes over.
		return actorImageReplace

	case mode == modeSourceAuthoritative:
		return actorImageReplace

	default:
		return actorImageOperatorOwned
	}
}

// note renders the SAFE ledger note for a decision — no filename, no key, no
// source content, because a ledger note is read by whoever reads the ledger.
//
// The carried variant's pixel size is in it on purpose: "which of the source's
// resolutions did you pick" is the exact question this bug made someone ask of a
// finished migration, and answering it from the ledger beats re-deriving it.
func (t actorImageTarget) note() string {
	switch t.action {
	case actorImageWrite:
		return "carried the " + t.variant() + " variant"
	case actorImageReplace:
		return "replaced the " + t.imageKind + " this import wrote earlier with the " + t.variant() + " variant"
	case actorImageUpToDate:
		return "the " + t.imageKind + " on this instance is already this source image"
	case actorImageOperatorOwned:
		return "left unchanged: the " + t.imageKind + " on this instance was not written by the import"
	case actorImageCleared:
		return "left unchanged: the " + t.imageKind + " the import wrote was removed on this instance"
	}
	return ""
}

// variant names the chosen source row by its pixel size, or by its id when the
// source records no size for it.
func (t actorImageTarget) variant() string {
	if t.img.Width > 0 && t.img.Height > 0 {
		return strconv.Itoa(t.img.Width) + "x" + strconv.Itoa(t.img.Height)
	}
	return "source-image-" + t.sourceID
}

// conflict returns the operator-facing note for a decision that leaves a slot
// alone. Divergence nobody is told about is the failure this pass exists to
// prevent, so both leave-alone outcomes say so in the report.
func (a actorImageAction) conflict(imageKind, sourceID string) (string, bool) {
	switch a {
	case actorImageOperatorOwned:
		return fmt.Sprintf("source actor image %s: this account/channel already has a %s the import did not write; it is left unchanged (the import only ever updates its own)", sourceID, imageKind), true
	case actorImageCleared:
		return fmt.Sprintf("source actor image %s: the %s the import wrote was removed on this instance; it is left unchanged", sourceID, imageKind), true
	}
	return "", false
}

// actorImageFingerprint identifies the exact object the import put in a slot.
// The storage key alone would not do it: the key is derived from the Vidra id
// and the extension, so re-uploading a different picture of the same type lands
// on the same key. Key + stored size distinguishes them, and both are recorded
// by the write itself, so nothing has to be re-read to compute it.
func actorImageFingerprint(img profileimage.Image) string {
	if img.StorageKey == "" {
		return ""
	}
	return img.StorageKey + "|" + strconv.FormatInt(img.SizeBytes, 10)
}

// importMode resolves the run's mode from the importer's options.
func (im *Importer) importMode() importMode {
	if im.sourceAuthoritative {
		return modeSourceAuthoritative
	}
	return modeGapFill
}

// ── the pass ──

// actorImageTarget is one source image resolved against this Vidra instance:
// which slot it fills, whose it is, and what the run decided to do with it.
type actorImageTarget struct {
	img       SourceActorImage
	sourceID  string
	ledgerKnd string
	imageKind string // profileimage.KindAvatar / KindBanner
	userID    uuid.UUID
	channelID uuid.UUID
	action    actorImageAction
}

// owner is the Vidra row the slot belongs to.
func (t actorImageTarget) owner() uuid.UUID {
	if t.userID != uuid.Nil {
		return t.userID
	}
	return t.channelID
}

// importActorImages carries account + channel avatars and banners.
//
// It runs regardless of --media-mode=reference, which is the one place this
// family departs from every other. Reference mode works for video because Vidra
// can point at the object keys the source already has in the shared bucket;
// avatars are not in that bucket on any PeerTube configuration, so there is
// nothing to reference. The choice is between fetching them and an instance
// whose accounts have no faces, and an operator who asked to reference existing
// media did not ask for that. --media-mode=none is respected: it says "import no
// media", and this is media.
func (im *Importer) importActorImages(ctx context.Context, r *Report) error {
	images, present, err := im.src.ActorImages(ctx)
	if err != nil {
		return err
	}
	if !present {
		r.Deferred = append(r.Deferred, "account + channel avatars/banners (this source has no actorImage table)")
		return nil
	}
	if im.mediaMode == MediaModeNone || im.destMedia == nil {
		r.Deferred = append(r.Deferred, "account + channel avatars/banners (media import is off)")
		return nil
	}
	origin, err := im.sourceOrigin(ctx)
	if err != nil {
		return err
	}
	if origin == "" {
		// Not a failure of the run: a source whose actors carry no absolute URL
		// cannot be asked for its images, and every other family still imports.
		r.Deferred = append(r.Deferred, "account + channel avatars/banners (the source's own actors carry no absolute URL, so its public origin is unknown)")
		return nil
	}

	svc := profileimage.NewService(im.q, im.destMedia)
	targets, err := im.resolveActorImageTargets(ctx, svc, images, r)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}

	fetcher := newLazyStaticFetcher(origin, lazyStaticAvatars)
	im.logger.InfoContext(ctx, "peertube import: fetching actor images",
		"origin", origin, "images", len(targets), "concurrency", lazyStaticConcurrency)

	// Bounded concurrency: a few connections to one live production host, not a
	// thundering herd. A per-image failure is recorded and the run continues —
	// nobody's migration should stop because one avatar 404s.
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		work = make(chan actorImageTarget)
	)
	for i := 0; i < lazyStaticConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range work {
				err := im.importOneActorImage(ctx, fetcher, svc, t, &mu, r)
				if err == nil {
					continue
				}
				im.markActorImageFailed(ctx, t, safeErr(err))
				mu.Lock()
				r.count(t.ledgerKnd).Failed++
				mu.Unlock()
				im.logger.WarnContext(ctx, "peertube import: actor image failed",
					"source_id", t.sourceID, "kind", t.imageKind, "error", err)
			}
		}()
	}
	for _, t := range targets {
		select {
		case work <- t:
		case <-ctx.Done():
			close(work)
			wg.Wait()
			return ctx.Err()
		}
	}
	close(work)
	wg.Wait()
	return nil
}

// resolveActorImageTargets turns the source rows into the work this run will
// actually do, recording a ledger row for everything it decides NOT to fetch.
// Every decision that can be made from the database is made here, before a
// single HTTP request leaves the machine — the source is live, and the most
// considerate request is the one that is never sent.
func (im *Importer) resolveActorImageTargets(
	ctx context.Context,
	svc *profileimage.Service,
	images []SourceActorImage,
	r *Report,
) ([]actorImageTarget, error) {
	mode := im.importMode()
	var targets []actorImageTarget
	for _, img := range images {
		imageKind, ok := mapActorImageKind(img.Type)
		if !ok {
			// Unknown ActorImageType: it belongs in a slot this tool cannot name.
			// Recorded under the avatar kind purely so the row is visible.
			sid := strconv.FormatInt(img.ID, 10)
			if done, err := im.actorImageRuledOut(ctx, KindActorAvatar, sid); err != nil {
				return nil, err
			} else if done {
				continue
			}
			if err := im.recordStandalone(ctx, KindActorAvatar, sid, uuid.Nil, "unsupported", "unrecognised actor image type"); err != nil {
				return nil, err
			}
			r.count(KindActorAvatar).Unsupported++
			continue
		}
		ledgerKnd := KindActorAvatar
		if imageKind == profileimage.KindBanner {
			ledgerKnd = KindActorBanner
		}
		sid := strconv.FormatInt(img.ID, 10)
		// Only 'unsupported' short-circuits, and only because it is a fact about
		// the SOURCE FILE — it cannot be an image, or it cannot fit, and no amount
		// of re-asking will change that. Every other status describes a decision
		// about the SLOT, and the slot is re-read below: a re-run must be able to
		// notice that what it wrote is no longer there.
		if ruled, err := im.actorImageRuledOut(ctx, ledgerKnd, sid); err != nil {
			return nil, err
		} else if ruled {
			r.count(ledgerKnd).Skipped++
			continue
		}
		if strings.TrimSpace(img.Filename) == "" {
			if err := im.recordStandalone(ctx, ledgerKnd, sid, uuid.Nil, "unsupported", "actor image row records no filename"); err != nil {
				return nil, err
			}
			r.count(ledgerKnd).Unsupported++
			continue
		}

		t := actorImageTarget{img: img, sourceID: sid, ledgerKnd: ledgerKnd, imageKind: imageKind}
		switch {
		case img.UserID != nil:
			id, ok, err := im.resolveParent(ctx, KindUser, strconv.FormatInt(*img.UserID, 10))
			if err != nil {
				return nil, err
			}
			if !ok {
				// No ledger row: the owner may be imported by a later run (the
				// same rule the per-video families follow), and then the image
				// should follow it in.
				r.count(ledgerKnd).Skipped++
				continue
			}
			t.userID = id
		case img.ChannelID != nil:
			id, ok, err := im.resolveParent(ctx, KindChannel, strconv.FormatInt(*img.ChannelID, 10))
			if err != nil {
				return nil, err
			}
			if !ok {
				r.count(ledgerKnd).Skipped++
				continue
			}
			t.channelID = id
		default:
			// A local actor that is neither an account with a user nor a channel:
			// the instance's own system actor. Nothing in Vidra owns that slot.
			if err := im.recordStandalone(ctx, ledgerKnd, sid, uuid.Nil, "skipped", "actor is neither an account nor a channel"); err != nil {
				return nil, err
			}
			r.count(ledgerKnd).Skipped++
			continue
		}

		slot, err := im.actorImageSlotState(ctx, svc, t)
		if err != nil {
			return nil, err
		}
		t.action = decideActorImage(slot, mode)
		switch t.action {
		case actorImageWrite, actorImageReplace:
			targets = append(targets, t)
		default:
			// Up to date, operator-owned or cleared: all three write no image. The
			// ledger row still lands, carrying the fingerprint memory forward
			// unchanged, so a run that decided to leave a slot alone never costs the
			// import the ability to recognise its own earlier write.
			if err := im.recordActorImage(ctx, t, slot.carried); err != nil {
				return nil, err
			}
			r.count(ledgerKnd).Skipped++
			if note, ok := t.action.conflict(imageKind, sid); ok {
				r.addConflict(note)
			}
		}
	}
	return targets, nil
}

// markActorImageFailed records a per-image failure WITHOUT downgrading a row
// that already records a completed write.
//
// The distinction matters because a completed row IS the slot's ownership
// memory. A run that re-carries a file (the heal-once path re-fetches the same
// id) and hits a 500 has changed nothing about the slot: the earlier write is
// still done and its image is still there. Stamping 'failed' over it would erase
// the only evidence that the import owns the slot, and the NEXT run would read a
// person's avatar where there is none and refuse to touch it forever. The
// failure is still in the report and the log; the row stays true, and because it
// stays true the retry still happens.
func (im *Importer) markActorImageFailed(ctx context.Context, t actorImageTarget, note string) {
	row, err := im.q.GetImportLedgerEntry(ctx, sqlcgen.GetImportLedgerEntryParams{
		EntityKind: t.ledgerKnd, SourceID: t.sourceID,
	})
	if err == nil && row.Status == "done" {
		return
	}
	im.markFailed(ctx, t.ledgerKnd, t.sourceID, note)
}

// actorImageRuledOut reports whether the ledger already records this source FILE
// as one that cannot be carried.
func (im *Importer) actorImageRuledOut(ctx context.Context, kind, sourceID string) (bool, error) {
	row, err := im.q.GetImportLedgerEntry(ctx, sqlcgen.GetImportLedgerEntryParams{EntityKind: kind, SourceID: sourceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return row.Status == "unsupported", nil
}

// actorImageSlotState gathers this instance's side of the decision for one slot:
// what is in it, and what the import remembers putting there.
func (im *Importer) actorImageSlotState(ctx context.Context, svc *profileimage.Service, t actorImageTarget) (actorImageSlot, error) {
	var (
		cur profileimage.Image
		err error
	)
	if t.userID != uuid.Nil {
		cur, err = svc.UserImage(ctx, t.userID, t.imageKind)
	} else {
		cur, err = svc.ChannelImage(ctx, t.channelID, t.imageKind)
	}
	var s actorImageSlot
	if err == nil {
		s.present, s.current = true, actorImageFingerprint(cur)
	}
	row, err := im.q.GetImportLedgerEntry(ctx, sqlcgen.GetImportLedgerEntryParams{
		EntityKind: t.ledgerKnd, SourceID: t.sourceID,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return s, err
	}
	s.carriedThisFile = err == nil && row.Status == "done"

	last, err := im.q.GetImportLedgerLastWriteForTarget(ctx, sqlcgen.GetImportLedgerLastWriteForTargetParams{
		EntityKind: t.ledgerKnd, VidraID: optUUID(t.owner()),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	s.carried, s.wroteBefore = last.AppliedValue, true
	s.untouched = s.present && !cur.UpdatedAt.After(last.UpdatedAt)
	return s, nil
}

// recordActorImage upserts a slot's ledger row, preserving the fingerprint
// memory it is given. Status follows the decision: only a file that cannot be
// carried is 'unsupported'; a slot left alone is 'skipped', which this pass
// re-reads on every run precisely so a later divergence is noticed.
//
// 'up to date' is 'done' and not 'skipped', which is not a cosmetic choice: the
// status is how the NEXT run knows this file is the one already in the slot, and
// downgrading it would make every subsequent run re-fetch an image it already
// has, forever.
func (im *Importer) recordActorImage(ctx context.Context, t actorImageTarget, applied string) error {
	status := "skipped"
	switch t.action {
	case actorImageWrite, actorImageReplace, actorImageUpToDate:
		status = "done"
	}
	return im.q.UpsertImportLedgerApplied(ctx, sqlcgen.UpsertImportLedgerAppliedParams{
		EntityKind:   t.ledgerKnd,
		SourceID:     t.sourceID,
		VidraID:      optUUID(t.owner()),
		Status:       status,
		Note:         t.note(),
		AppliedValue: applied,
	})
}

// importOneActorImage fetches one image and writes it through the normal
// profile-image path, so it lands on whatever backend the instance stores media
// on, under the same key layout an upload would use.
func (im *Importer) importOneActorImage(
	ctx context.Context,
	fetcher *lazyStaticFetcher,
	svc *profileimage.Service,
	t actorImageTarget,
	mu *sync.Mutex,
	r *Report,
) error {
	body, ext, err := fetcher.fetch(ctx, t.img.Filename)
	if err != nil {
		// Two facts about the source, not transient failures: the bytes are not an
		// image Vidra stores, or there are too many of them. Recording either one
		// terminal stops the next run asking the same question again.
		var reason string
		switch {
		case errors.Is(err, errNotAnImage):
			reason = "source did not serve a JPEG, PNG or WebP"
		case errors.Is(err, errImageTooLarge):
			reason = fmt.Sprintf("source image is larger than the %d-byte cap this import accepts", int64(maxLazyStaticBytes))
		default:
			return err
		}
		if err := im.recordStandalone(ctx, t.ledgerKnd, t.sourceID, uuid.Nil, "unsupported", reason); err != nil {
			return err
		}
		mu.Lock()
		r.count(t.ledgerKnd).Unsupported++
		mu.Unlock()
		return nil
	}

	// The filename here exists only to carry the extension: profileimage derives
	// both the stored content type and the object key's suffix from it, and the
	// extension came from sniffing the bytes above.
	in := profileimage.UploadInput{Filename: "import" + ext, Reader: bytes.NewReader(body)}
	var stored profileimage.Image
	if t.userID != uuid.Nil {
		stored, err = svc.SetUserImage(ctx, t.userID, t.imageKind, in)
	} else {
		stored, err = svc.SetChannelImage(ctx, t.channelID, t.imageKind, in)
	}
	if err != nil {
		return err
	}
	// The ledger row lands after the write rather than inside it: the blob and
	// the image row are not one transaction (no blob write ever is). A crash in
	// the gap leaves the image correctly in place with no ledger row, and the
	// next run sees a slot holding something it cannot claim and leaves it alone,
	// reporting it — the picture is right either way, only the note is less
	// precise. What it carries is the FINGERPRINT of what was just written, which
	// is what lets every later run tell this write apart from a person's upload.
	if err := im.recordActorImage(ctx, t, actorImageFingerprint(stored)); err != nil {
		return err
	}
	mu.Lock()
	r.count(t.ledgerKnd).Imported++
	mu.Unlock()
	return nil
}

// sourceOrigin resolves (once per importer) the source instance's public origin
// from its own actors' canonical URLs.
func (im *Importer) sourceOrigin(ctx context.Context) (string, error) {
	im.originOnce.Do(func() {
		urls, err := im.src.LocalActorURLs(ctx)
		if err != nil {
			im.originErr = err
			return
		}
		im.origin = deriveSourceOrigin(urls)
	})
	return im.origin, im.originErr
}

// planActorImages adds the two actor-image families to a dry-run plan: one entry
// per SLOT a local actor has an image for, which is what the source read already
// reduces to. It deliberately stops there and does not run the ownership
// decision — a plan is taken before the users and channels exist, so every slot
// would resolve to "no owner yet" and the plan would report a migration that
// carries no faces at all.
func (im *Importer) planActorImages(ctx context.Context, r *Report) error {
	images, present, err := im.src.ActorImages(ctx)
	if err != nil {
		return err
	}
	if !present {
		r.Deferred = append(r.Deferred, "account + channel avatars/banners (this source has no actorImage table)")
		return nil
	}
	for _, img := range images {
		kind, ok := mapActorImageKind(img.Type)
		if !ok {
			continue
		}
		if kind == profileimage.KindBanner {
			r.count(KindActorBanner).Planned++
		} else {
			r.count(KindActorAvatar).Planned++
		}
	}
	if im.mediaMode == MediaModeNone {
		r.Deferred = append(r.Deferred, "account + channel avatars/banners (media import is off)")
		return nil
	}
	origin, err := im.sourceOrigin(ctx)
	if err != nil {
		return err
	}
	if origin == "" {
		r.Deferred = append(r.Deferred, "account + channel avatars/banners (the source's own actors carry no absolute URL, so its public origin is unknown)")
	}
	return nil
}
