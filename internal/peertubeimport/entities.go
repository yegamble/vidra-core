package peertubeimport

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// This file holds the per-entity importers. Each processes one source family,
// parent-first, and is idempotent: a source entity with a terminal ledger row
// (done/skipped/unsupported) from a prior run is not re-imported.

// ── lookups used for conflict detection + parent resolution ──

func (im *Importer) findUserByUsername(ctx context.Context, username string) (uuid.UUID, bool, error) {
	id, err := im.q.ImportFindUserByUsername(ctx, username)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}

func (im *Importer) findUserByEmail(ctx context.Context, email string) (uuid.UUID, bool, error) {
	id, err := im.q.ImportFindUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}

func (im *Importer) handleExists(ctx context.Context, handle string) (bool, error) {
	_, err := im.q.ImportFindChannelByHandle(ctx, handle)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (im *Importer) usernameExists(ctx context.Context, username string) (bool, error) {
	_, ok, err := im.findUserByUsername(ctx, username)
	return ok, err
}

func (im *Importer) emailExists(ctx context.Context, email string) (bool, error) {
	_, ok, err := im.findUserByEmail(ctx, email)
	return ok, err
}

// recordStandalone upserts a ledger row outside a transaction (used when the
// import links a source entity to an existing Vidra row instead of inserting).
func (im *Importer) recordStandalone(ctx context.Context, kind, sourceID string, vidraID uuid.UUID, status, note string) error {
	return recordLedger(ctx, im.q, kind, sourceID, vidraID, status, note)
}

// markFailed records a per-row failure in the ledger with a SAFE note.
func (im *Importer) markFailed(ctx context.Context, kind, sourceID, note string) {
	_ = recordLedger(ctx, im.q, kind, sourceID, uuid.Nil, "failed", note)
}

// ── users ──

func (im *Importer) userConflict(ctx context.Context, u SourceUser) (string, bool, error) {
	if ok, err := im.usernameExists(ctx, u.Username); err != nil {
		return "", false, err
	} else if ok {
		return fmt.Sprintf("username %q already exists (policy=%s)", u.Username, im.policy), true, nil
	}
	if ok, err := im.emailExists(ctx, u.Email); err != nil {
		return "", false, err
	} else if ok {
		return fmt.Sprintf("email for user %q already exists (policy=%s)", u.Username, im.policy), true, nil
	}
	return "", false, nil
}

// userPlan is the decision for one source user after conflict resolution.
type userPlan struct {
	insert   bool
	username string
	email    string
	note     string
	status   string    // ledger status for a link (skip='skipped', merge='done')
	linkTo   uuid.UUID // existing Vidra user when not inserting
}

func (im *Importer) decideUser(ctx context.Context, u SourceUser) (userPlan, error) {
	unameID, unameHit, err := im.findUserByUsername(ctx, u.Username)
	if err != nil {
		return userPlan{}, err
	}
	emailID, emailHit, err := im.findUserByEmail(ctx, u.Email)
	if err != nil {
		return userPlan{}, err
	}
	if !unameHit && !emailHit {
		return userPlan{insert: true, username: u.Username, email: u.Email, status: "done"}, nil
	}
	switch im.policy {
	case PolicyFail:
		return userPlan{}, ErrConflictFail
	case PolicyRename:
		username, email, note := u.Username, u.Email, ""
		if unameHit {
			username, err = im.dedupeUsername(ctx, u.Username)
			if err != nil {
				return userPlan{}, err
			}
			note = "renamed username to " + username
		}
		if emailHit {
			email, err = im.dedupeEmail(ctx, u.Email)
			if err != nil {
				return userPlan{}, err
			}
			note = strings.TrimPrefix(note+"; renamed email", "; ")
		}
		return userPlan{insert: true, username: username, email: email, note: note, status: "done"}, nil
	case PolicyMerge:
		target := unameID
		if !unameHit {
			target = emailID
		}
		return userPlan{insert: false, linkTo: target, status: "done", note: "merged into existing user"}, nil
	default: // PolicySkip
		target := unameID
		if !unameHit {
			target = emailID
		}
		return userPlan{insert: false, linkTo: target, status: "skipped", note: "username or email already exists"}, nil
	}
}

func (im *Importer) importUsers(ctx context.Context, r *Report) error {
	users, err := im.src.Users(ctx)
	if err != nil {
		return err
	}
	c := r.count(KindUser)
	for _, u := range users {
		sid := strconv.FormatInt(u.ID, 10)
		// A source-authoritative run answers from the destination snapshot FIRST:
		// it owns an account for this source user, so the question is not "have I
		// imported this?" but "have the two sides diverged?" — and that is a map
		// lookup, not a query. handled=false falls through to the gap-filling path
		// below, which is untouched and is still what every default run does.
		if im.resync != nil {
			handled, err := im.resyncOneUser(ctx, u, sid, r, c)
			if err != nil {
				im.markFailed(ctx, KindUser, sid, safeErr(err))
				c.Failed++
				im.logger.WarnContext(ctx, "peertube import: user resync failed", "source_id", sid, "error", err)
				continue
			}
			if handled {
				continue
			}
		}
		if _, _, done, err := im.alreadyProcessed(ctx, KindUser, sid); err != nil {
			return err
		} else if done {
			c.Skipped++
			continue
		}
		if err := im.importOneUser(ctx, u, r, c); err != nil {
			if errors.Is(err, ErrConflictFail) {
				return err
			}
			im.markFailed(ctx, KindUser, sid, safeErr(err))
			c.Failed++
			im.logger.WarnContext(ctx, "peertube import: user failed", "source_id", sid, "error", err)
		}
	}
	return nil
}

func (im *Importer) importOneUser(ctx context.Context, u SourceUser, r *Report, c *Counts) error {
	sid := strconv.FormatInt(u.ID, 10)
	plan, err := im.decideUser(ctx, u)
	if err != nil {
		return err
	}
	if !plan.insert {
		if err := im.recordStandalone(ctx, KindUser, sid, plan.linkTo, plan.status, plan.note); err != nil {
			return err
		}
		r.addConflict("user " + u.Username + ": " + plan.note)
		c.Skipped++
		return nil
	}
	sealedPriv, err := im.sealPrivateKey(u.PrivateKeyPEM)
	if err != nil {
		return err
	}
	err = im.withTx(ctx, func(q *sqlcgen.Queries) error {
		id, err := q.ImportInsertUser(ctx, sqlcgen.ImportInsertUserParams{
			Username:      plan.username,
			Email:         plan.email,
			PasswordHash:  u.PasswordHash,
			Role:          mapRole(u.Role),
			EmailVerified: u.EmailVerified,
			IsActive:      true,
			DisplayName:   u.DisplayName,
			CreatedAt:     u.CreatedAt,
		})
		if err != nil {
			return err
		}
		if u.PublicKeyPEM != "" && sealedPriv != "" {
			if err := q.ImportUpsertAccountActorKey(ctx, sqlcgen.ImportUpsertAccountActorKeyParams{
				UserID:        id,
				PublicKeyPem:  u.PublicKeyPEM,
				PrivateKeyPem: sealedPriv,
			}); err != nil {
				return err
			}
		}
		return recordLedger(ctx, q, KindUser, sid, id, "done", plan.note)
	})
	if err != nil {
		return err
	}
	if plan.note != "" {
		r.addConflict("user " + u.Username + ": " + plan.note)
	}
	c.Imported++
	return nil
}

// ── channels ──

func (im *Importer) importChannels(ctx context.Context, r *Report) error {
	channels, err := im.src.Channels(ctx)
	if err != nil {
		return err
	}
	c := r.count(KindChannel)
	for _, ch := range channels {
		sid := strconv.FormatInt(ch.ID, 10)
		if im.resync != nil {
			handled, err := im.resyncOneChannel(ctx, ch, sid, r, c)
			if err != nil {
				im.markFailed(ctx, KindChannel, sid, safeErr(err))
				c.Failed++
				im.logger.WarnContext(ctx, "peertube import: channel resync failed", "source_id", sid, "error", err)
				continue
			}
			if handled {
				continue
			}
		}
		if _, _, done, err := im.alreadyProcessed(ctx, KindChannel, sid); err != nil {
			return err
		} else if done {
			c.Skipped++
			continue
		}
		if err := im.importOneChannel(ctx, ch, r, c); err != nil {
			if errors.Is(err, ErrConflictFail) {
				return err
			}
			im.markFailed(ctx, KindChannel, sid, safeErr(err))
			c.Failed++
			im.logger.WarnContext(ctx, "peertube import: channel failed", "source_id", sid, "error", err)
		}
	}
	return nil
}

func (im *Importer) importOneChannel(ctx context.Context, ch SourceChannel, r *Report, c *Counts) error {
	sid := strconv.FormatInt(ch.ID, 10)
	owner, ok, err := im.resolveParent(ctx, KindUser, strconv.FormatInt(ch.OwnerUserID, 10))
	if err != nil {
		return err
	}
	if !ok {
		// Owner was not imported (skipped/failed/absent) — cannot attach a channel.
		if err := im.recordStandalone(ctx, KindChannel, sid, uuid.Nil, "skipped", "owner user not imported"); err != nil {
			return err
		}
		c.Skipped++
		return nil
	}

	handle := ch.Handle
	note := ""
	exists, err := im.handleExists(ctx, handle)
	if err != nil {
		return err
	}
	if exists {
		switch im.policy {
		case PolicyFail:
			return ErrConflictFail
		case PolicyRename:
			handle, err = im.dedupeHandle(ctx, ch.Handle)
			if err != nil {
				return err
			}
			note = "renamed handle to " + handle
		default: // skip / merge → link to the existing channel
			existingID, _, err := im.findChannelByHandle(ctx, ch.Handle)
			if err != nil {
				return err
			}
			status := "skipped"
			linkNote := "channel handle already exists"
			if im.policy == PolicyMerge {
				status = "done"
				linkNote = "merged into existing channel"
			}
			if err := im.recordStandalone(ctx, KindChannel, sid, existingID, status, linkNote); err != nil {
				return err
			}
			r.addConflict("channel " + ch.Handle + ": " + linkNote)
			c.Skipped++
			return nil
		}
	}

	sealedPriv, err := im.sealPrivateKey(ch.PrivateKeyPEM)
	if err != nil {
		return err
	}
	err = im.withTx(ctx, func(q *sqlcgen.Queries) error {
		id, err := q.ImportInsertChannel(ctx, sqlcgen.ImportInsertChannelParams{
			OwnerID:     owner,
			Handle:      handle,
			DisplayName: ch.DisplayName,
			Description: ch.Description,
			CreatedAt:   ch.CreatedAt,
		})
		if err != nil {
			return err
		}
		if ch.PublicKeyPEM != "" && sealedPriv != "" {
			if err := q.ImportUpsertChannelActorKey(ctx, sqlcgen.ImportUpsertChannelActorKeyParams{
				ChannelID:     id,
				PublicKeyPem:  ch.PublicKeyPEM,
				PrivateKeyPem: sealedPriv,
			}); err != nil {
				return err
			}
		}
		return recordLedger(ctx, q, KindChannel, sid, id, "done", note)
	})
	if err != nil {
		return err
	}
	if note != "" {
		r.addConflict("channel " + ch.Handle + ": " + note)
	}
	c.Imported++
	return nil
}

func (im *Importer) findChannelByHandle(ctx context.Context, handle string) (uuid.UUID, bool, error) {
	id, err := im.q.ImportFindChannelByHandle(ctx, handle)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}

// ── videos (+ metadata, file, thumbnail, captions, tags) ──

func (im *Importer) importVideos(ctx context.Context, r *Report) error {
	videos, err := im.src.Videos(ctx)
	if err != nil {
		return err
	}
	c := r.count(KindVideo)
	for _, v := range videos {
		sid := v.UUID
		if im.resync != nil {
			handled, err := im.resyncOneVideo(ctx, v, r, c)
			if err != nil {
				im.markFailed(ctx, KindVideo, sid, safeErr(err))
				c.Failed++
				im.logger.WarnContext(ctx, "peertube import: video resync failed", "source_uuid", sid, "error", err)
				continue
			}
			if handled {
				continue
			}
		}
		if _, _, done, err := im.alreadyProcessed(ctx, KindVideo, sid); err != nil {
			return err
		} else if done {
			c.Skipped++
			continue
		}
		if err := im.importOneVideo(ctx, v, r, c); err != nil {
			im.markFailed(ctx, KindVideo, sid, safeErr(err))
			c.Failed++
			im.logger.WarnContext(ctx, "peertube import: video failed", "source_uuid", sid, "error", err)
		}
	}
	return nil
}

func (im *Importer) importOneVideo(ctx context.Context, v SourceVideo, r *Report, c *Counts) error {
	channel, ok, err := im.resolveParent(ctx, KindChannel, strconv.FormatInt(v.ChannelID, 10))
	if err != nil {
		return err
	}
	if !ok {
		if err := im.recordStandalone(ctx, KindVideo, v.UUID, uuid.Nil, "skipped", "channel not imported"); err != nil {
			return err
		}
		c.Skipped++
		return nil
	}

	// Copy media OUTSIDE the transaction (blob writes cannot be transactional);
	// Put overwrites, so a re-run after a crash safely re-copies. The DB rows +
	// ledger then commit atomically, so the ledger 'done' implies the blobs exist.
	var (
		fileKey, fileType, fileName string
		fileSize                    int64
		// Only copy mode produces a digest — it is the hash of the stream the
		// copy already had to read. Reference mode writes no bytes, so the row
		// lands unhashed and the backfill worker hashes the referenced object.
		fileSHA      string
		haveFile     bool
		hlsMasterKey string
		haveHLS      bool
	)
	files, err := im.src.VideoFiles(ctx, v.ID)
	if err != nil {
		return err
	}
	// Media keys are opaque, unique object paths (stored verbatim in
	// video_files.storage_key / captions.storage_key and served from there), so a
	// fresh random id is fine and avoids any dependence on the row's own id.
	mediaID := uuid.New()
	if len(files) > 0 && im.srcMedia != nil && im.destMedia != nil {
		f := files[0]
		if !allowedVideoExt[strings.ToLower(f.Extname)] {
			im.logger.WarnContext(ctx, "peertube import: skipping video file with disallowed extension", "source_uuid", v.UUID)
		} else {
			switch im.mediaMode {
			case MediaModeCopy:
				fileKey = "web-videos/" + mediaID.String() + f.Extname
				size, sum, cerr := im.copyMedia(ctx, sourceWebVideoKey(f.Filename), fileKey)
				if cerr != nil {
					return cerr
				}
				fileSize = size
				fileSHA = sum
				fileType = contentTypeForExt(f.Extname)
				fileName = f.Filename
				haveFile = true
			case MediaModeReference:
				fileKey = sourceWebVideoKey(f.Filename)
				fileSize = f.Size
				fileType = contentTypeForExt(f.Extname)
				fileName = f.Filename
				haveFile = true
			}
		}
	}
	if len(files) > 0 && im.mediaMode == MediaModeReference && !haveFile {
		f := files[0]
		if allowedVideoExt[strings.ToLower(f.Extname)] {
			fileKey = sourceWebVideoKey(f.Filename)
			fileSize = f.Size
			fileType = contentTypeForExt(f.Extname)
			fileName = f.Filename
			haveFile = true
		}
	}

	// NOTE: the poster is NOT written here. It used to be, pointing at
	// thumbnails/<peertube-filename> in the source's object store — a key
	// PeerTube never writes, because thumbnails are not one of the five families
	// its object storage covers. It is carried by importVideoThumbnails instead,
	// which fetches it and can also backfill onto videos this run does not touch;
	// see entities_videoimages.go.

	captions, err := im.src.Captions(ctx, v.ID)
	if err != nil {
		return err
	}
	type capCopy struct{ lang, key string }
	var copiedCaps []capCopy
	if im.mediaMode == MediaModeCopy && im.srcMedia != nil && im.destMedia != nil {
		for _, capt := range captions {
			if !allowedCaptionExt[extOf(capt.Filename)] {
				continue
			}
			key := "captions/" + mediaID.String() + "/" + capt.Language + ".vtt"
			if _, _, cerr := im.copyMedia(ctx, sourceCaptionKey(capt.Filename), key); cerr != nil {
				return cerr
			}
			copiedCaps = append(copiedCaps, capCopy{lang: capt.Language, key: key})
		}
	}
	if im.mediaMode == MediaModeReference {
		for _, capt := range captions {
			if !allowedCaptionExt[extOf(capt.Filename)] {
				continue
			}
			copiedCaps = append(copiedCaps, capCopy{lang: capt.Language, key: sourceCaptionKey(capt.Filename)})
		}
	}

	if im.mediaMode == MediaModeReference {
		if hls, ok, err := im.src.HLSPlaylist(ctx, v.ID); err != nil {
			return err
		} else if ok {
			hlsMasterKey = sourceHLSKey(v.UUID, hls.PlaylistFilename)
			haveHLS = true
		}
	}

	tags, err := im.src.Tags(ctx, v.ID)
	if err != nil {
		return err
	}

	err = im.withTx(ctx, func(q *sqlcgen.Queries) error {
		id, err := q.ImportInsertVideo(ctx, sqlcgen.ImportInsertVideoParams{
			ChannelID:   channel,
			Title:       v.Title,
			Description: v.Description,
			Privacy:     mapPrivacy(v.Privacy),
			State:       mapVideoState(v.State),
			Category:    intPtrToText(v.Category),
			Language:    v.Language,
			License:     intPtrToText(v.Licence),
			CreatedAt:   v.CreatedAt,
		})
		if err != nil {
			return err
		}
		if v.Duration > 0 {
			d := int32(v.Duration)
			if err := q.ImportUpsertVideoMetadata(ctx, sqlcgen.ImportUpsertVideoMetadataParams{
				VideoID:         id,
				DurationSeconds: &d,
			}); err != nil {
				return err
			}
		}
		if haveFile {
			if _, err := q.ImportInsertVideoFile(ctx, sqlcgen.ImportInsertVideoFileParams{
				VideoID:      id,
				Kind:         "original",
				StorageKey:   fileKey,
				ContentType:  fileType,
				OriginalName: fileName,
				SizeBytes:    fileSize,
				Sha256:       fileSHA,
			}); err != nil {
				return err
			}
		}
		if haveHLS {
			if _, err := q.UpsertStreamingPlaylist(ctx, sqlcgen.UpsertStreamingPlaylistParams{
				VideoID:   id,
				MasterKey: hlsMasterKey,
				State:     "ready",
			}); err != nil {
				return err
			}
		}
		for _, cc := range copiedCaps {
			if _, err := q.ImportUpsertCaption(ctx, sqlcgen.ImportUpsertCaptionParams{
				VideoID:    id,
				Language:   cc.lang,
				StorageKey: cc.key,
			}); err != nil {
				return err
			}
		}
		for _, tag := range tags {
			t := normalizeTag(tag)
			if t == "" {
				continue
			}
			if err := q.ImportInsertVideoTag(ctx, sqlcgen.ImportInsertVideoTagParams{VideoID: id, Tag: t}); err != nil {
				return err
			}
		}
		return recordLedger(ctx, q, KindVideo, v.UUID, id, "done", "")
	})
	if err != nil {
		return err
	}
	c.Imported++
	if haveFile {
		r.count(KindVideoFile).Imported++
	}
	if haveHLS {
		r.count(KindHLSPlaylist).Imported++
	}
	r.count(KindCaption).Imported += len(copiedCaps)
	for _, tag := range tags {
		if normalizeTag(tag) != "" {
			r.count(KindTag).Imported++
		}
	}
	return nil
}

// ── comments (threaded) ──

func (im *Importer) importComments(ctx context.Context, r *Report) error {
	comments, err := im.src.Comments(ctx)
	if err != nil {
		return err
	}
	c := r.count(KindComment)
	for _, cm := range comments {
		sid := strconv.FormatInt(cm.ID, 10)
		if _, _, done, err := im.alreadyProcessed(ctx, KindComment, sid); err != nil {
			return err
		} else if done {
			c.Skipped++
			continue
		}
		if err := im.importOneComment(ctx, cm, c); err != nil {
			im.markFailed(ctx, KindComment, sid, safeErr(err))
			c.Failed++
			im.logger.WarnContext(ctx, "peertube import: comment failed", "source_id", sid, "error", err)
		}
	}
	return nil
}

func (im *Importer) importOneComment(ctx context.Context, cm SourceComment, c *Counts) error {
	sid := strconv.FormatInt(cm.ID, 10)
	// The ledger keys videos by UUID; resolve the comment's numeric videoId to the
	// video's Vidra id through the cached numeric→uuid map.
	videoVidraID, ok, err := im.resolveVideoByNumericID(ctx, cm.VideoID)
	if err != nil {
		return err
	}
	if !ok {
		if err := im.recordStandalone(ctx, KindComment, sid, uuid.Nil, "skipped", "video not imported"); err != nil {
			return err
		}
		c.Skipped++
		return nil
	}
	author, ok, err := im.resolveParent(ctx, KindUser, strconv.FormatInt(cm.AuthorUser, 10))
	if err != nil {
		return err
	}
	if !ok {
		if err := im.recordStandalone(ctx, KindComment, sid, uuid.Nil, "skipped", "author not imported"); err != nil {
			return err
		}
		c.Skipped++
		return nil
	}
	var parent pgtype.UUID
	if cm.ParentID != 0 {
		if pid, ok, err := im.resolveParent(ctx, KindComment, strconv.FormatInt(cm.ParentID, 10)); err != nil {
			return err
		} else if ok {
			parent = optUUID(pid)
		}
	}
	body := strings.TrimSpace(cm.Body)
	if body == "" {
		if err := im.recordStandalone(ctx, KindComment, sid, uuid.Nil, "skipped", "empty body"); err != nil {
			return err
		}
		c.Skipped++
		return nil
	}
	return im.withTx(ctx, func(q *sqlcgen.Queries) error {
		id, err := q.ImportInsertComment(ctx, sqlcgen.ImportInsertCommentParams{
			VideoID:   videoVidraID,
			UserID:    optUUID(author),
			Body:      body,
			ParentID:  parent,
			CreatedAt: cm.CreatedAt,
		})
		if err != nil {
			return err
		}
		c.Imported++
		return recordLedger(ctx, q, KindComment, sid, id, "done", "")
	})
}

// sourceVideosByID returns the source's local videos keyed by numeric id,
// reading them from the source once per importer and caching the result. Every
// family that references a video by its numeric id goes through this.
func (im *Importer) sourceVideosByID(ctx context.Context) (map[int64]SourceVideo, error) {
	if im.videosByID != nil {
		return im.videosByID, nil
	}
	videos, err := im.src.Videos(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]SourceVideo, len(videos))
	for _, v := range videos {
		byID[v.ID] = v
	}
	im.videosByID = byID
	return byID, nil
}

// resolveVideoByNumericID maps a source video numeric id to its Vidra id. The
// ledger keys videos by UUID, so we translate the numeric id to the UUID via the
// source, then look up the ledger.
//
// The answer is memoised for the run. Every family that hangs off a video asks
// this, once per row — on a real instance that is ~100,000 asks for ~15,000
// distinct videos, and the video ledger cannot move underneath them because the
// video pass has already finished by the time any of them runs.
func (im *Importer) resolveVideoByNumericID(ctx context.Context, numericID int64) (uuid.UUID, bool, error) {
	if id, ok := im.videoVidraByID[numericID]; ok {
		return id, id != uuid.Nil, nil
	}
	byID, err := im.sourceVideosByID(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	v, ok := byID[numericID]
	if !ok {
		return uuid.Nil, false, nil
	}
	id, found, err := im.resolveParent(ctx, KindVideo, v.UUID)
	if err != nil {
		return uuid.Nil, false, err
	}
	if !found {
		id = uuid.Nil
	}
	if im.videoVidraByID == nil {
		im.videoVidraByID = map[int64]uuid.UUID{}
	}
	im.videoVidraByID[numericID] = id
	return id, found, nil
}

// ── playlists (+ elements) ──

func (im *Importer) importPlaylists(ctx context.Context, r *Report) error {
	playlists, err := im.src.Playlists(ctx)
	if err != nil {
		return err
	}
	c := r.count(KindPlaylist)
	for _, p := range playlists {
		sid := strconv.FormatInt(p.ID, 10)
		if im.resync != nil {
			handled, err := im.resyncOnePlaylist(ctx, p, sid, r, c)
			if err != nil {
				im.markFailed(ctx, KindPlaylist, sid, safeErr(err))
				c.Failed++
				im.logger.WarnContext(ctx, "peertube import: playlist resync failed", "source_id", sid, "error", err)
				continue
			}
			if handled {
				continue
			}
		}
		if _, _, done, err := im.alreadyProcessed(ctx, KindPlaylist, sid); err != nil {
			return err
		} else if done {
			c.Skipped++
			continue
		}
		if err := im.importOnePlaylist(ctx, p, r, c); err != nil {
			im.markFailed(ctx, KindPlaylist, sid, safeErr(err))
			c.Failed++
			im.logger.WarnContext(ctx, "peertube import: playlist failed", "source_id", sid, "error", err)
		}
	}
	return nil
}

func (im *Importer) importOnePlaylist(ctx context.Context, p SourcePlaylist, r *Report, c *Counts) error {
	sid := strconv.FormatInt(p.ID, 10)
	owner, ok, err := im.resolveParent(ctx, KindUser, strconv.FormatInt(p.OwnerUserID, 10))
	if err != nil {
		return err
	}
	if !ok {
		if err := im.recordStandalone(ctx, KindPlaylist, sid, uuid.Nil, "skipped", "owner not imported"); err != nil {
			return err
		}
		c.Skipped++
		return nil
	}
	els, err := im.src.PlaylistElements(ctx, p.ID)
	if err != nil {
		return err
	}
	items := 0
	err = im.withTx(ctx, func(q *sqlcgen.Queries) error {
		id, err := q.ImportInsertPlaylist(ctx, sqlcgen.ImportInsertPlaylistParams{
			OwnerID:     owner,
			Title:       p.Title,
			Description: p.Description,
			Visibility:  mapPlaylistPrivacy(p.Privacy),
			CreatedAt:   p.CreatedAt,
		})
		if err != nil {
			return err
		}
		for _, el := range els {
			videoID, ok, err := im.resolveVideoByNumericID(ctx, el.VideoID)
			if err != nil {
				return err
			}
			if !ok {
				continue // video not imported → drop the slot
			}
			if err := q.ImportInsertPlaylistItem(ctx, sqlcgen.ImportInsertPlaylistItemParams{
				PlaylistID: id,
				VideoID:    videoID,
				Position:   int32(el.Position),
			}); err != nil {
				return err
			}
			items++
		}
		return recordLedger(ctx, q, KindPlaylist, sid, id, "done", "")
	})
	if err != nil {
		return err
	}
	c.Imported++
	r.count(KindPlaylistItem).Imported += items
	return nil
}

// ── follows / subscriptions ──

func (im *Importer) importFollows(ctx context.Context, r *Report) error {
	follows, err := im.src.Follows(ctx)
	if err != nil {
		return err
	}
	c := r.count(KindFollow)
	for _, f := range follows {
		sid := fmt.Sprintf("%d:%d", f.FollowerUserID, f.ChannelID)
		if _, _, done, err := im.alreadyProcessed(ctx, KindFollow, sid); err != nil {
			return err
		} else if done {
			c.Skipped++
			continue
		}
		if err := im.importOneFollow(ctx, f, sid, c); err != nil {
			im.markFailed(ctx, KindFollow, sid, safeErr(err))
			c.Failed++
			im.logger.WarnContext(ctx, "peertube import: follow failed", "source_id", sid, "error", err)
		}
	}
	// An UNSUBSCRIBE is not visible in the loop above — it is the absence of a row
	// — so it is carried by diffing what the source offers against what the ledger
	// records the import having created. Only those pairs are removed: a
	// subscription somebody made on this instance has no ledger row and is
	// untouched.
	if im.resync != nil {
		if err := im.resyncRemovedFollows(ctx, follows, c); err != nil {
			return err
		}
	}
	return nil
}

func (im *Importer) importOneFollow(ctx context.Context, f SourceFollow, sid string, c *Counts) error {
	follower, ok, err := im.resolveParent(ctx, KindUser, strconv.FormatInt(f.FollowerUserID, 10))
	if err != nil {
		return err
	}
	if !ok {
		if err := im.recordStandalone(ctx, KindFollow, sid, uuid.Nil, "skipped", "follower not imported"); err != nil {
			return err
		}
		c.Skipped++
		return nil
	}
	channel, ok, err := im.resolveParent(ctx, KindChannel, strconv.FormatInt(f.ChannelID, 10))
	if err != nil {
		return err
	}
	if !ok {
		if err := im.recordStandalone(ctx, KindFollow, sid, uuid.Nil, "skipped", "channel not imported"); err != nil {
			return err
		}
		c.Skipped++
		return nil
	}
	return im.withTx(ctx, func(q *sqlcgen.Queries) error {
		if err := q.ImportFollowChannel(ctx, sqlcgen.ImportFollowChannelParams{FollowerID: follower, ChannelID: channel}); err != nil {
			return err
		}
		c.Imported++
		// A follow has no natural Vidra id; record the mapping keyed by the pair.
		return recordLedger(ctx, q, KindFollow, sid, channel, "done", "")
	})
}

// ── dedupe helpers (rename policy) ──

func (im *Importer) dedupeUsername(ctx context.Context, base string) (string, error) {
	return dedupeName(base, func(cand string) (bool, error) { return im.usernameExists(ctx, cand) })
}

func (im *Importer) dedupeHandle(ctx context.Context, base string) (string, error) {
	return dedupeName(base, func(cand string) (bool, error) { return im.handleExists(ctx, cand) })
}

func (im *Importer) dedupeEmail(ctx context.Context, base string) (string, error) {
	at := strings.LastIndex(base, "@")
	if at < 0 {
		return dedupeName(base, func(cand string) (bool, error) { return im.emailExists(ctx, cand) })
	}
	local, domain := base[:at], base[at:]
	for n := 2; n < 10000; n++ {
		cand := fmt.Sprintf("%s+import%d%s", local, n, domain)
		exists, err := im.emailExists(ctx, cand)
		if err != nil {
			return "", err
		}
		if !exists {
			return cand, nil
		}
	}
	return "", fmt.Errorf("peertubeimport: exhausted email rename candidates for %q", base)
}

// safeErr renders an error as a SHORT, safe ledger note. It never includes the
// source DSN or credentials (those are not part of query errors) and is capped.
func safeErr(err error) string {
	const max = 200
	msg := err.Error()
	if len(msg) > max {
		msg = msg[:max]
	}
	return msg
}

// normalizeTag lowercases + trims a source tag to Vidra's video_tags CHECK
// (lowercase, length 1–50); returns "" when it cannot fit.
func normalizeTag(tag string) string {
	t := strings.ToLower(strings.TrimSpace(tag))
	if t == "" || len(t) > 50 {
		return ""
	}
	return t
}

// contentTypeForExt maps a media extension to a stored content type.
func contentTypeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".mov":
		return "video/quicktime"
	case ".ogv":
		return "video/ogg"
	case ".avi":
		return "video/x-msvideo"
	default:
		return "application/octet-stream"
	}
}

func imageContentTypeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}
