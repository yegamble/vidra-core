package peertubeimport

import (
	"context"
	"crypto/sha256"
	"fmt"
	"hash"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// ── source-authoritative resync ──
//
// The default import FILLS GAPS. Every write it makes is an INSERT guarded by
// ON CONFLICT DO NOTHING, every entity is frozen behind a terminal ledger row
// after its first run, and an avatar or a chapter set that already exists here is
// left exactly as it is. That is the right default: an import that overwrites is
// an import that can quietly undo somebody's work.
//
// It is not, however, the workflow this tool is actually used for. The operator
// runs it against a STILL-LIVE PeerTube on a schedule, for days, right up to
// cutover — and in that window titles get edited, passwords get changed, videos
// get made public, chapters get moved, playlists get re-ordered and people
// unsubscribe. Gap-filling carries none of it, so the instance the operator
// cuts over to is the source as it stood on the first run.
//
// Options.SourceAuthoritative says the source wins. This file is what that
// means.
//
// ── the organising principle: the ledger IS the provenance record ──
//
// peertube_import_ledger maps every source entity to the Vidra row that stands
// for it. So "did the import write this row?" is answerable for every family,
// and that gives the mode a safe definition: IT UPDATES EXACTLY THE ROWS THE
// IMPORT CREATED, AND NEVER TOUCHES ANYTHING THAT WAS ALREADY ON THIS INSTANCE.
// A video somebody uploaded here has no ledger row and is invisible to every
// query below. An account created here is invisible. A channel created here is
// invisible.
//
// The presence of a ledger row is NOT on its own that answer, and reading it as
// though it were is what 0124 closes: --conflict-policy skip/merge maps a source
// entity onto a row that already existed here — merge records that mapping as
// 'done' — so the reads below also require created_by_import, which the two link
// paths in entities.go state FALSE at creation and nothing re-asserts later.
//
// ── what it will not do, ever ──
//
//  1. It NEVER re-INSERTs a parent entity. ImportInsertVideo is a plain INSERT
//     with no ON CONFLICT and no pre-check, so re-running it would create a
//     duplicate video row, and the ledger upsert would then repoint vidra_id at
//     the duplicate — orphaning the original row, its children and its blobs.
//     Every write here is an UPDATE keyed on an id that came out of the ledger.
//  2. It NEVER rotates an actor keypair. account_actor_keys / channel_actor_keys
//     are written ON CONFLICT DO NOTHING and stay that way: rewriting a live
//     federation keypair invalidates every outstanding HTTP signature, and the
//     source's key is not "newer", it is the same key. ImportUpdateUser and
//     ImportUpdateChannel do not name those tables at all.
//  3. It NEVER assigns a view count. video_view_counts holds the source's
//     lifetime total PLUS every view Vidra has served since, with no stored
//     decomposition, so assigning the source total would destroy Vidra-native
//     data. The view pass already tracks the source correctly, by applying the
//     DELTA (see entities_pervideo.go); this mode does not touch it.
//  4. It NEVER overwrites a rendition row. A Vidra re-transcode writes its own
//     rendition rows against its own key prefixes, and the ledger cannot tell
//     those apart from the ones the import wrote — ImportInsertVideoRendition is
//     DO NOTHING, so a 'done' ledger row does not prove a write landed. The
//     ladder is media, and media only changes when the media changes.
//  5. It NEVER re-downloads media for a metadata change. Nothing below opens the
//     source object store or makes an HTTP request. (Actor images are the one
//     media family this mode does reach, and they have their own decision
//     function that spends a fetch only where the two sides actually differ —
//     see decideActorImage.)
//
// ── how an unchanged entity stays free ──
//
// A no-op re-run of this importer costs ~21 seconds on a 155k-entity catalogue,
// and that number is the cutover's downtime budget. A resync that asked the
// destination one question per entity would turn it into minutes over an SSH
// tunnel. So the destination is read in BULK — one statement per family for the
// whole instance, keyed by source id — before the passes run, and an unchanged
// entity then costs one map lookup and nothing else. No extra query, no source
// round trip, no write. The source side is bulk for the same reason: the two
// families the older passes read per-entity (tags, playlist elements) get bulk
// reads of their own here rather than 14,766 round trips.

// digestScheme versions the field lists below. Everything hashed here is
// (scheme, family, fields…), so BUMPING IT re-syncs every entity exactly once —
// which is precisely what should happen when a release starts carrying a field
// it did not carry before, and is why the scheme is in the hash rather than
// being an unwritten assumption.
const digestScheme = "vidra-peertube-resync/v1"

// digest summarises a set of mapped fields as a fixed-length, CONTENT-FREE
// value. Content-free matters: these are compared, and a comparison needs no
// plaintext — and anything the import remembers could end up in a ledger note or
// a log, where a video title is noise and an email address is a leak.
//
// Every field is length-prefixed before it is absorbed, so ("ab","c") and
// ("a","bc") are different digests. Without that, moving a character across a
// field boundary would be invisible, which for a title/description pair is not a
// theoretical concern.
type digest struct{ h hash.Hash }

func newDigest(family string) *digest {
	d := &digest{h: sha256.New()}
	return d.text(digestScheme).text(family)
}

func (d *digest) text(s string) *digest {
	_, _ = d.h.Write([]byte(strconv.Itoa(len(s))))
	_, _ = d.h.Write([]byte{':'})
	_, _ = d.h.Write([]byte(s))
	_, _ = d.h.Write([]byte{0})
	return d
}

func (d *digest) num(n int64) *digest { return d.text(strconv.FormatInt(n, 10)) }

func (d *digest) flag(b bool) *digest { return d.text(strconv.FormatBool(b)) }

func (d *digest) id(u uuid.UUID) *digest { return d.text(u.String()) }

// nullTime folds an OPTIONAL timestamp. The empty string stands for NULL and
// cannot collide with a formatted instant, so "never published elsewhere" and
// any real date stay distinguishable. The value is normalised to UTC before it
// is formatted: both sides come out of a timestamptz, and a difference in the
// session time zone the two were read under is not a change.
func (d *digest) nullTime(t *time.Time) *digest {
	if t == nil {
		return d.text("")
	}
	return d.text(t.UTC().Format(time.RFC3339Nano))
}

func (d *digest) sum() string { return fmt.Sprintf("%x", d.h.Sum(nil)) }

// ── the per-family field lists ──
//
// Each of these is called with the SOURCE's mapped values and with the
// DESTINATION's current values, and the two are compared. That is the whole of
// change detection, and it is deliberately not PeerTube's updatedAt:
//
//   - updatedAt moves for reasons that change nothing here (a view counter tick,
//     a federation refresh), so it would spend a full write on every entity on
//     every run — the opposite of the 21-second re-run this has to preserve;
//   - it is not on every schema in the accepted range, and this importer's rule
//     is to probe a source column, never assume it;
//   - and it only ever describes the SOURCE. Comparing the two sides catches
//     divergence in BOTH directions, which is what the operator actually asked
//     for: an edit made on Vidra is reverted by the next run, because under this
//     mode the source is the truth. A stored "what I last wrote" fingerprint
//     could not see that edit at all.

func userDigest(passwordHash, role, displayName string, emailVerified bool) string {
	return newDigest("user").text(passwordHash).text(role).text(displayName).flag(emailVerified).sum()
}

func channelDigest(owner uuid.UUID, displayName, description string) string {
	return newDigest("channel").id(owner).text(displayName).text(description).sum()
}

func videoDigest(channel uuid.UUID, title, description, privacy, state, category, language, license string, duration int32, originallyPublishedAt *time.Time) string {
	return newDigest("video").id(channel).text(title).text(description).
		text(privacy).text(state).text(category).text(language).text(license).num(int64(duration)).
		nullTime(originallyPublishedAt).sum()
}

// tagSetDigest folds a video's tags. The caller passes them normalised, sorted
// and de-duplicated, because video_tags is a SET keyed (video_id, tag) and the
// order rows come back in is not information.
func tagSetDigest(tags []string) string {
	d := newDigest("video_tags").num(int64(len(tags)))
	for _, t := range tags {
		d.text(t)
	}
	return d.sum()
}

// chapterMark is one seek-bar mark, in the shape both sides reduce to.
type chapterMark struct {
	start int32
	title string
}

func chapterSetDigest(marks []chapterMark) string {
	d := newDigest("chapters").num(int64(len(marks)))
	for _, m := range marks {
		d.num(int64(m.start)).text(m.title)
	}
	return d.sum()
}

func playlistDigest(owner uuid.UUID, title, description, visibility string) string {
	return newDigest("playlist").id(owner).text(title).text(description).text(visibility).sum()
}

// playlistSlot is one video's place in a playlist. Position is part of it: a
// re-ordered playlist is a changed playlist.
type playlistSlot struct {
	video    uuid.UUID
	position int32
}

func playlistItemsDigest(slots []playlistSlot) string {
	d := newDigest("playlist_items").num(int64(len(slots)))
	for _, s := range slots {
		d.id(s.video).num(int64(s.position))
	}
	return d.sum()
}

// ── the destination snapshot ──

type resyncUser struct {
	id       uuid.UUID
	username string
	email    string
	dgst     string
}

type resyncChannel struct {
	id     uuid.UUID
	owner  uuid.UUID
	handle string
	dgst   string
}

type resyncVideo struct {
	id uuid.UUID
	// channel, duration and origPub are carried alongside the digest because they
	// are the fields the source may have NO OPINION about — a channel it never
	// imported, a duration it does not record, an original-publication date it
	// has no column for at all — and a comparison has to be made against the
	// value already standing here or every run would rewrite it. For origPub the
	// stake is higher than a rewrite: a source older than PeerTube's
	// originallyPublishedAt reports nil for every video, and letting that win
	// would erase the whole catalogue's dates on the first source-authoritative
	// run.
	channel  uuid.UUID
	duration int32
	origPub  *time.Time
	dgst     string
}

type resyncPlaylist struct {
	id    uuid.UUID
	owner uuid.UUID
	dgst  string
}

// ratingKey is video_ratings' own primary key, which is the only identity a
// rating has on this side.
type ratingKey struct {
	user  uuid.UUID
	video uuid.UUID
}

// resyncState is everything the destination currently holds for the rows the
// import owns, read once per run in nine bulk statements. It is a SNAPSHOT taken
// before the passes run: entities this same run creates are absent from it,
// which is correct — a row that did not exist cannot have diverged.
type resyncState struct {
	users         map[string]resyncUser
	channels      map[string]resyncChannel
	videos        map[string]resyncVideo
	videoTags     map[string]string // source video uuid -> tag-set digest
	chapters      map[string]string // source video uuid -> chapter-set digest
	ratings       map[ratingKey]string
	ratingOwned   map[string]string // source rating id -> the provenance the import recorded
	playlists     map[string]resyncPlaylist
	playlistItems map[string]string // source playlist id -> item-set digest
	follows       map[string]uuid.UUID
}

// loadResyncState reads the destination's side of every family this mode
// reconciles. Nine statements for the whole instance; nothing per entity.
func (im *Importer) loadResyncState(ctx context.Context) (*resyncState, error) {
	st := &resyncState{
		users:         map[string]resyncUser{},
		channels:      map[string]resyncChannel{},
		videos:        map[string]resyncVideo{},
		videoTags:     map[string]string{},
		chapters:      map[string]string{},
		ratings:       map[ratingKey]string{},
		ratingOwned:   map[string]string{},
		playlists:     map[string]resyncPlaylist{},
		playlistItems: map[string]string{},
		follows:       map[string]uuid.UUID{},
	}

	users, err := im.q.ImportResyncUsers(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		st.users[u.SourceID] = resyncUser{
			id: u.ID, username: u.Username, email: u.Email,
			dgst: userDigest(u.PasswordHash, u.Role, u.DisplayName, u.EmailVerified),
		}
	}

	channels, err := im.q.ImportResyncChannels(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range channels {
		st.channels[c.SourceID] = resyncChannel{
			id: c.ID, owner: c.OwnerID, handle: c.Handle,
			dgst: channelDigest(c.OwnerID, c.DisplayName, c.Description),
		}
	}

	videos, err := im.q.ImportResyncVideos(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range videos {
		origPub := video.TimePtr(v.OriginallyPublishedAt)
		st.videos[v.SourceID] = resyncVideo{
			id: v.ID, channel: v.ChannelID, duration: v.DurationSeconds, origPub: origPub,
			dgst: videoDigest(v.ChannelID, v.Title, v.Description, v.Privacy, v.State,
				v.Category, v.Language, v.License, v.DurationSeconds, origPub),
		}
	}

	// Tags and chapters come back as rows ordered by (source id, member) and are
	// folded HERE, by the same functions the source side is folded by. Folding
	// them in SQL instead would mean two implementations of one definition, and
	// any disagreement between them reads as a change that is not there — a
	// rewrite of every video's tags, every night.
	tagRows, err := im.q.ImportResyncVideoTags(ctx)
	if err != nil {
		return nil, err
	}
	forEachRun(len(tagRows), func(i int) string { return tagRows[i].SourceID }, func(sid string, lo, hi int) {
		tags := make([]string, 0, hi-lo)
		for i := lo; i < hi; i++ {
			tags = append(tags, tagRows[i].Tag)
		}
		st.videoTags[sid] = tagSetDigest(tags)
	})

	chapterRows, err := im.q.ImportResyncChapters(ctx)
	if err != nil {
		return nil, err
	}
	forEachRun(len(chapterRows), func(i int) string { return chapterRows[i].SourceID }, func(sid string, lo, hi int) {
		marks := make([]chapterMark, 0, hi-lo)
		for i := lo; i < hi; i++ {
			marks = append(marks, chapterMark{start: chapterRows[i].StartSeconds, title: chapterRows[i].Title})
		}
		st.chapters[sid] = chapterSetDigest(marks)
	})

	ratings, err := im.q.ImportResyncRatings(ctx)
	if err != nil {
		return nil, err
	}
	for _, rate := range ratings {
		st.ratings[ratingKey{user: rate.UserID, video: rate.VideoID}] = rate.Rating
	}
	ratingLedger, err := im.q.ListImportLedgerDoneByKind(ctx, KindRating)
	if err != nil {
		return nil, err
	}
	for _, row := range ratingLedger {
		if row.AppliedValue != "" {
			st.ratingOwned[row.SourceID] = row.AppliedValue
		}
	}

	playlists, err := im.q.ImportResyncPlaylists(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range playlists {
		st.playlists[p.SourceID] = resyncPlaylist{
			id: p.ID, owner: p.OwnerID,
			dgst: playlistDigest(p.OwnerID, p.Title, p.Description, p.Visibility),
		}
	}
	itemRows, err := im.q.ImportResyncPlaylistItems(ctx)
	if err != nil {
		return nil, err
	}
	forEachRun(len(itemRows), func(i int) string { return itemRows[i].SourceID }, func(sid string, lo, hi int) {
		slots := make([]playlistSlot, 0, hi-lo)
		for i := lo; i < hi; i++ {
			slots = append(slots, playlistSlot{video: itemRows[i].VideoID, position: itemRows[i].Position})
		}
		st.playlistItems[sid] = playlistItemsDigest(slots)
	})

	followLedger, err := im.q.ListImportLedgerDoneByKind(ctx, KindFollow)
	if err != nil {
		return nil, err
	}
	for _, row := range followLedger {
		if row.VidraID.Valid {
			st.follows[row.SourceID] = uuid.UUID(row.VidraID.Bytes)
		}
	}
	return st, nil
}

// forEachRun walks a slice ordered by a key and calls fn once per run of equal
// keys with its [lo,hi) bounds — the fold that turns member rows back into per-
// entity sets without building an intermediate map of slices.
func forEachRun(n int, keyAt func(int) string, fn func(key string, lo, hi int)) {
	for lo := 0; lo < n; {
		hi := lo + 1
		for hi < n && keyAt(hi) == keyAt(lo) {
			hi++
		}
		fn(keyAt(lo), lo, hi)
		lo = hi
	}
}

// ── users ──

// resyncOneUser reconciles one source user against the Vidra account the import
// created for it. handled=false means the import does not own an account for
// this source user, so the normal (insert-or-link) path must run.
func (im *Importer) resyncOneUser(ctx context.Context, u SourceUser, sid string, r *Report, c *Counts) (bool, error) {
	cur, owned := im.resync.users[sid]
	if !owned {
		return false, nil
	}
	// The natural keys are compared BEFORE the digest short-circuit, so a standing
	// divergence is reported on every run rather than being invisible because
	// nothing else about the account moved. They are never written: username and
	// email are uniquely indexed and are what the conflict policy exists to
	// resolve, so carrying a source-side rename blindly could collide with an
	// unrelated account this import has nothing to do with.
	if !strings.EqualFold(cur.username, u.Username) {
		r.addConflict(fmt.Sprintf("user %q was renamed to %q on the source; the username here is left unchanged (it is the natural key --conflict-policy owns, and rewriting it could collide with another account)", cur.username, u.Username))
	}
	if !strings.EqualFold(cur.email, u.Email) {
		r.addConflict(fmt.Sprintf("user %q changed email address on the source; the address here is left unchanged (it is a login identifier and a uniquely indexed natural key)", cur.username))
	}
	desired := userDigest(u.PasswordHash, mapRole(u.Role), u.DisplayName, u.EmailVerified)
	if desired == cur.dgst {
		c.Skipped++
		return true, nil
	}
	// The actor keypair is deliberately absent from this write. Rotating a live
	// federation key invalidates every HTTP signature already in flight, and the
	// source's key is not a newer key — it is the same one.
	if err := im.withTx(ctx, func(q *sqlcgen.Queries) error {
		if err := q.ImportUpdateUser(ctx, sqlcgen.ImportUpdateUserParams{
			ID:            cur.id,
			PasswordHash:  u.PasswordHash,
			Role:          mapRole(u.Role),
			EmailVerified: u.EmailVerified,
			DisplayName:   u.DisplayName,
		}); err != nil {
			return err
		}
		return recordLedger(ctx, q, KindUser, sid, cur.id, "done", "resynced from the source")
	}); err != nil {
		return true, err
	}
	c.Updated++
	return true, nil
}

// ── channels ──

func (im *Importer) resyncOneChannel(ctx context.Context, ch SourceChannel, sid string, r *Report, c *Counts) (bool, error) {
	cur, owned := im.resync.channels[sid]
	if !owned {
		return false, nil
	}
	if !strings.EqualFold(cur.handle, ch.Handle) {
		r.addConflict(fmt.Sprintf("channel %q was renamed to %q on the source; the handle here is left unchanged (it is the natural key --conflict-policy owns, and it is this instance's public URL for the channel)", cur.handle, ch.Handle))
	}
	// An owner the import cannot resolve is not an opinion: the source's account
	// for it was never imported, so the channel keeps the owner it has rather than
	// being detached from one.
	owner := cur.owner
	if id, ok, err := im.resolveParent(ctx, KindUser, strconv.FormatInt(ch.OwnerUserID, 10)); err != nil {
		return true, err
	} else if ok {
		owner = id
	}
	desired := channelDigest(owner, ch.DisplayName, ch.Description)
	if desired == cur.dgst {
		c.Skipped++
		return true, nil
	}
	if err := im.withTx(ctx, func(q *sqlcgen.Queries) error {
		if err := q.ImportUpdateChannel(ctx, sqlcgen.ImportUpdateChannelParams{
			ID:          cur.id,
			OwnerID:     owner,
			DisplayName: ch.DisplayName,
			Description: ch.Description,
		}); err != nil {
			return err
		}
		return recordLedger(ctx, q, KindChannel, sid, cur.id, "done", "resynced from the source")
	}); err != nil {
		return true, err
	}
	c.Updated++
	return true, nil
}

// ── videos (metadata + the tag set) ──

func (im *Importer) resyncOneVideo(ctx context.Context, v SourceVideo, r *Report, c *Counts) (bool, error) {
	cur, owned := im.resync.videos[v.UUID]
	if !owned {
		return false, nil
	}
	// A channel the import never brought over is not an instruction to detach the
	// video: it keeps the channel it has. Same for a source that records no
	// duration — the value standing here is carried into the comparison rather
	// than being overwritten with a zero on every run.
	channel := cur.channel
	if id, ok, err := im.resolveParent(ctx, KindChannel, strconv.FormatInt(v.ChannelID, 10)); err != nil {
		return true, err
	} else if ok {
		channel = id
	}
	duration := int32(v.Duration)
	haveDuration := v.Duration > 0
	if !haveDuration {
		duration = cur.duration
	}
	// Same rule for the original-publication date: a source that records none
	// (or has no column for it at all) is not saying "clear it", so the value
	// standing here is what the comparison and the write both use.
	origPub := v.OriginallyPublishedAt
	if origPub == nil {
		origPub = cur.origPub
	}
	desired := videoDigest(channel, v.Title, v.Description, mapPrivacy(v.Privacy), mapVideoState(v.State),
		pgconv.Deref(intPtrToText(v.Category)), pgconv.Deref(v.Language), pgconv.Deref(intPtrToText(v.Licence)), duration, origPub)

	tags, err := im.desiredTags(ctx, v.ID)
	if err != nil {
		return true, err
	}
	tagsChanged := tagSetDigest(tags) != im.resync.videoTags[v.UUID]

	if desired == cur.dgst && !tagsChanged {
		c.Skipped++
		return true, nil
	}
	if err := im.withTx(ctx, func(q *sqlcgen.Queries) error {
		if desired != cur.dgst {
			if err := q.ImportUpdateVideo(ctx, sqlcgen.ImportUpdateVideoParams{
				ID:          cur.id,
				ChannelID:   channel,
				Title:       v.Title,
				Description: v.Description,
				Privacy:     mapPrivacy(v.Privacy),
				State:       mapVideoState(v.State),
				Category:    intPtrToText(v.Category),
				Language:    v.Language,
				License:     intPtrToText(v.Licence),

				OriginallyPublishedAt: optTimestamptz(origPub),
			}); err != nil {
				return err
			}
			if haveDuration {
				// Duration only — ImportUpsertVideoMetadata would also write the
				// width and height the import does not have, erasing whatever a
				// Vidra transcode recorded there.
				if err := q.ImportUpdateVideoDuration(ctx, sqlcgen.ImportUpdateVideoDurationParams{
					VideoID: cur.id, DurationSeconds: &duration,
				}); err != nil {
					return err
				}
			}
		}
		if tagsChanged {
			for _, t := range tags {
				if err := q.ImportInsertVideoTag(ctx, sqlcgen.ImportInsertVideoTagParams{VideoID: cur.id, Tag: t}); err != nil {
					return err
				}
			}
			// video_tags is a SET, so carrying the source means removing what the
			// source no longer has. An empty list removes them all, which is what a
			// video whose tags were cleared upstream actually looks like.
			if err := q.ImportDeleteVideoTagsNotIn(ctx, sqlcgen.ImportDeleteVideoTagsNotInParams{
				VideoID: cur.id, Tags: tags,
			}); err != nil {
				return err
			}
		}
		return recordLedger(ctx, q, KindVideo, v.UUID, cur.id, "done", "resynced from the source")
	}); err != nil {
		return true, err
	}
	if desired != cur.dgst {
		c.Updated++
	} else {
		c.Skipped++
	}
	if tagsChanged {
		r.count(KindTag).Updated += len(tags)
	}
	return true, nil
}

// desiredTags is the source's tag set for one video, normalised to what
// video_tags will accept, de-duplicated and sorted — the same shape the
// destination side is folded into.
func (im *Importer) desiredTags(ctx context.Context, sourceVideoID int64) ([]string, error) {
	all, err := im.sourceTags(ctx)
	if err != nil {
		return nil, err
	}
	return normalizeTagSet(all[sourceVideoID]), nil
}

// normalizeTagSet applies normalizeTag to every entry, drops what cannot be
// stored, de-duplicates, and sorts. It always returns a non-nil slice so it can
// be handed straight to a `= ANY($1::text[])`.
func normalizeTagSet(raw []string) []string {
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		n := normalizeTag(t)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ── chapters ──

// chapterGroup is one source video's WHOLE chapter set. The set is the unit of
// comparison and of replacement, because video_chapters is keyed
// (video_id, start_seconds): a chapter somebody MOVED is a different row, so an
// upsert would leave the old mark standing and the video would show both.
type chapterGroup struct {
	videoID  int64
	chapters []SourceChapter
}

// groupChapters splits the source's chapter list into per-video runs. The source
// read is ordered by (videoId, timecode), so this is a walk, not a sort.
func groupChapters(chs []SourceChapter) []chapterGroup {
	var out []chapterGroup
	for i := 0; i < len(chs); {
		j := i + 1
		for j < len(chs) && chs[j].VideoID == chs[i].VideoID {
			j++
		}
		out = append(out, chapterGroup{videoID: chs[i].VideoID, chapters: chs[i:j]})
		i = j
	}
	return out
}

// desiredChapterSet reduces a source group to the marks Vidra can store: blank
// or over-long titles normalised, unstorable rows dropped (and counted), and at
// most one mark per start second — the primary key admits no more, and the
// source is read in timecode order so the first one wins, exactly as the
// DO NOTHING insert already behaves.
func desiredChapterSet(chs []SourceChapter) (marks []chapterMark, dropped []SourceChapter) {
	seen := map[int32]bool{}
	for _, ch := range chs {
		title := normalizeChapterTitle(ch.Title)
		if title == "" || ch.Timecode < 0 {
			dropped = append(dropped, ch)
			continue
		}
		start := int32(ch.Timecode)
		if seen[start] {
			dropped = append(dropped, ch)
			continue
		}
		seen[start] = true
		marks = append(marks, chapterMark{start: start, title: title})
	}
	return marks, dropped
}

// resyncVideoChapters replaces one video's chapter set when it has moved.
// handled=false means the import does not own this video, so the gap-filling
// per-chapter path runs instead and nothing here can reach a video somebody
// created on this instance.
func (im *Importer) resyncVideoChapters(ctx context.Context, g chapterGroup, c *Counts) (bool, error) {
	byID, err := im.sourceVideosByID(ctx)
	if err != nil {
		return false, err
	}
	sv, ok := byID[g.videoID]
	if !ok {
		return false, nil
	}
	cur, owned := im.resync.videos[sv.UUID]
	if !owned {
		return false, nil
	}
	marks, dropped := desiredChapterSet(g.chapters)
	if chapterSetDigest(marks) == im.resync.chapters[sv.UUID] {
		c.Skipped += len(g.chapters)
		return true, nil
	}
	if err := im.withTx(ctx, func(q *sqlcgen.Queries) error {
		// Delete-then-reinsert, in one transaction. It is the only shape that
		// cannot duplicate a moved mark, and the only one that can remove a mark
		// the source deleted.
		if err := q.ImportDeleteVideoChapters(ctx, cur.id); err != nil {
			return err
		}
		for _, m := range marks {
			if err := q.ImportInsertVideoChapter(ctx, sqlcgen.ImportInsertVideoChapterParams{
				VideoID: cur.id, StartSeconds: m.start, Title: m.title,
			}); err != nil {
				return err
			}
		}
		for _, ch := range g.chapters {
			sid := strconv.FormatInt(ch.ID, 10)
			status, note := "done", "resynced from the source"
			if containsChapter(dropped, ch.ID) {
				status, note = "unsupported", "chapter has no usable title or start, or repeats another mark's start"
			}
			if err := recordLedger(ctx, q, KindChapter, sid, cur.id, status, note); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return true, err
	}
	c.Updated += len(marks)
	c.Unsupported += len(dropped)
	return true, nil
}

func containsChapter(chs []SourceChapter, id int64) bool {
	for _, ch := range chs {
		if ch.ID == id {
			return true
		}
	}
	return false
}

// ── ratings ──

// ratingProvenance is what the ledger remembers about a rating the import wrote:
// the pair it wrote it for and the value it wrote. It is the ONLY way an unrate
// can be carried safely — a source row that has gone away carries no user, no
// video and no value, so without this there is nothing to delete and no way to
// tell whether deleting it would remove somebody's own vote.
func ratingProvenance(user, video uuid.UUID, rating string) string {
	return "u:" + user.String() + "|v:" + video.String() + "|r:" + rating
}

func parseRatingProvenance(s string) (user, video uuid.UUID, rating string, ok bool) {
	parts := strings.Split(s, "|")
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "u:") ||
		!strings.HasPrefix(parts[1], "v:") || !strings.HasPrefix(parts[2], "r:") {
		return uuid.Nil, uuid.Nil, "", false
	}
	u, err := uuid.Parse(strings.TrimPrefix(parts[0], "u:"))
	if err != nil {
		return uuid.Nil, uuid.Nil, "", false
	}
	v, err := uuid.Parse(strings.TrimPrefix(parts[1], "v:"))
	if err != nil {
		return uuid.Nil, uuid.Nil, "", false
	}
	return u, v, strings.TrimPrefix(parts[2], "r:"), true
}

// resyncOneRating carries a source rating, including the two shapes an UNRATE
// takes: PeerTube either leaves the row behind with type 'none' or deletes it
// outright, depending on its version. The first is handled here; the second by
// resyncRemovedRatings below.
func (im *Importer) resyncOneRating(ctx context.Context, rate SourceRating, sid string, r *Report, c *Counts) (bool, error) {
	videoID, ok, err := im.resolveVideoByNumericID(ctx, rate.VideoID)
	if err != nil {
		return true, err
	}
	if !ok {
		c.Skipped++
		return true, nil
	}
	userID, ok, err := im.resolveParent(ctx, KindUser, strconv.FormatInt(rate.RaterUser, 10))
	if err != nil {
		return true, err
	}
	if !ok {
		c.Skipped++
		return true, nil
	}
	key := ratingKey{user: userID, video: videoID}
	current := im.resync.ratings[key]
	value, mappable := mapRating(rate.Type)

	if !mappable {
		// 'none' — the source says this person has no opinion any more.
		if current == "" {
			// Nothing standing, nothing to carry. Not an unsupported ROW so much as
			// an absent one, and writing a ledger row for it every run would be work
			// the mode exists to avoid.
			c.Skipped++
			return true, nil
		}
		if !im.ownsRating(sid, key, current) {
			r.addConflict(fmt.Sprintf("source rating %s was cleared on the source, but the rating standing here was not written by the import; it is left unchanged", sid))
			c.Skipped++
			return true, nil
		}
		if err := im.withTx(ctx, func(q *sqlcgen.Queries) error {
			if err := q.ImportDeleteVideoRating(ctx, sqlcgen.ImportDeleteVideoRatingParams{
				UserID: userID, VideoID: videoID,
			}); err != nil {
				return err
			}
			return q.UpsertImportLedgerApplied(ctx, sqlcgen.UpsertImportLedgerAppliedParams{
				EntityKind: KindRating, SourceID: sid, VidraID: optUUID(videoID),
				Status: "skipped", Note: "the rating was cleared on the source", AppliedValue: "",
			})
		}); err != nil {
			return true, err
		}
		delete(im.resync.ratings, key)
		c.Updated++
		return true, nil
	}

	if current == value {
		c.Skipped++
		return true, nil
	}
	if current != "" && !im.ownsRating(sid, key, current) {
		r.addConflict(fmt.Sprintf("source rating %s differs from the rating standing here, which was not written by the import; it is left unchanged", sid))
		c.Skipped++
		return true, nil
	}
	if err := im.withTx(ctx, func(q *sqlcgen.Queries) error {
		if err := q.ImportUpsertVideoRating(ctx, sqlcgen.ImportUpsertVideoRatingParams{
			VideoID: videoID, UserID: userID, Rating: value, CreatedAt: rate.CreatedAt,
		}); err != nil {
			return err
		}
		return q.UpsertImportLedgerApplied(ctx, sqlcgen.UpsertImportLedgerAppliedParams{
			EntityKind: KindRating, SourceID: sid, VidraID: optUUID(videoID),
			Status: "done", Note: "", AppliedValue: ratingProvenance(userID, videoID, value),
		})
	}); err != nil {
		return true, err
	}
	im.resync.ratings[key] = value
	im.resync.ratingOwned[sid] = ratingProvenance(userID, videoID, value)
	if current == "" {
		c.Imported++
	} else {
		c.Updated++
	}
	return true, nil
}

// ownsRating reports whether the rating standing on this pair is the one the
// import last wrote. An instance migrated by a release that recorded no
// provenance has none of this memory, so its ratings are left alone and reported
// rather than removed on a guess — the memory fills in the first time a rating
// is carried.
func (im *Importer) ownsRating(sid string, key ratingKey, standing string) bool {
	prov, ok := im.resync.ratingOwned[sid]
	if !ok {
		return false
	}
	u, v, rating, parsed := parseRatingProvenance(prov)
	return parsed && u == key.user && v == key.video && rating == standing
}

// resyncRemovedRatings carries the other shape of an unrate: the source row is
// GONE. The ledger still maps it, and the provenance it recorded names the pair
// and the value — so a rating that still holds exactly what the import wrote is
// removed, and one that does not is somebody's own vote and is reported instead.
func (im *Importer) resyncRemovedRatings(ctx context.Context, ratings []SourceRating, r *Report, c *Counts) error {
	present := make(map[string]bool, len(ratings))
	for _, rate := range ratings {
		present[strconv.FormatInt(rate.ID, 10)] = true
	}
	sids := make([]string, 0, len(im.resync.ratingOwned))
	for sid := range im.resync.ratingOwned {
		sids = append(sids, sid)
	}
	sort.Strings(sids) // deterministic order, so a run's report reads the same twice
	for _, sid := range sids {
		if present[sid] {
			continue
		}
		user, video, rating, ok := parseRatingProvenance(im.resync.ratingOwned[sid])
		if !ok {
			continue
		}
		key := ratingKey{user: user, video: video}
		if im.resync.ratings[key] != rating {
			r.addConflict(fmt.Sprintf("source rating %s is gone from the source, but the rating standing here is no longer the one the import wrote; it is left unchanged", sid))
			continue
		}
		if err := im.withTx(ctx, func(q *sqlcgen.Queries) error {
			if err := q.ImportDeleteVideoRating(ctx, sqlcgen.ImportDeleteVideoRatingParams{
				UserID: user, VideoID: video,
			}); err != nil {
				return err
			}
			return q.UpsertImportLedgerApplied(ctx, sqlcgen.UpsertImportLedgerAppliedParams{
				EntityKind: KindRating, SourceID: sid, VidraID: optUUID(video),
				Status: "skipped", Note: "the rating no longer exists on the source", AppliedValue: "",
			})
		}); err != nil {
			im.markFailed(ctx, KindRating, sid, safeErr(err))
			c.Failed++
			im.logger.WarnContext(ctx, "peertube import: rating removal failed", "source_id", sid, "error", err)
			continue
		}
		delete(im.resync.ratings, key)
		delete(im.resync.ratingOwned, sid)
		c.Updated++
	}
	return nil
}

// ── playlists (metadata + the ordered item set) ──

func (im *Importer) resyncOnePlaylist(ctx context.Context, p SourcePlaylist, sid string, r *Report, c *Counts) (bool, error) {
	cur, owned := im.resync.playlists[sid]
	if !owned {
		return false, nil
	}
	owner := cur.owner
	if id, ok, err := im.resolveParent(ctx, KindUser, strconv.FormatInt(p.OwnerUserID, 10)); err != nil {
		return true, err
	} else if ok {
		owner = id
	}
	desired := playlistDigest(owner, p.Title, p.Description, mapPlaylistPrivacy(p.Privacy))

	els, err := im.sourcePlaylistElements(ctx, p.ID)
	if err != nil {
		return true, err
	}
	slots := make([]playlistSlot, 0, len(els))
	videoIDs := make([]uuid.UUID, 0, len(els))
	for _, el := range els {
		videoID, ok, err := im.resolveVideoByNumericID(ctx, el.VideoID)
		if err != nil {
			return true, err
		}
		if !ok {
			continue // the video is not on this instance; the slot cannot exist
		}
		slots = append(slots, playlistSlot{video: videoID, position: int32(el.Position)})
		videoIDs = append(videoIDs, videoID)
	}
	itemsChanged := playlistItemsDigest(slots) != im.resync.playlistItems[sid]

	if desired == cur.dgst && !itemsChanged {
		c.Skipped++
		return true, nil
	}
	if err := im.withTx(ctx, func(q *sqlcgen.Queries) error {
		if desired != cur.dgst {
			if err := q.ImportUpdatePlaylist(ctx, sqlcgen.ImportUpdatePlaylistParams{
				ID: cur.id, OwnerID: owner, Title: p.Title, Description: p.Description,
				Visibility: mapPlaylistPrivacy(p.Privacy),
			}); err != nil {
				return err
			}
		}
		if itemsChanged {
			for _, s := range slots {
				if err := q.ImportUpsertPlaylistItem(ctx, sqlcgen.ImportUpsertPlaylistItemParams{
					PlaylistID: cur.id, VideoID: s.video, Position: s.position,
				}); err != nil {
					return err
				}
			}
			if err := q.ImportDeletePlaylistItemsNotIn(ctx, sqlcgen.ImportDeletePlaylistItemsNotInParams{
				PlaylistID: cur.id, VideoIds: videoIDs,
			}); err != nil {
				return err
			}
		}
		return recordLedger(ctx, q, KindPlaylist, sid, cur.id, "done", "resynced from the source")
	}); err != nil {
		return true, err
	}
	if desired != cur.dgst {
		c.Updated++
	} else {
		c.Skipped++
	}
	if itemsChanged {
		r.count(KindPlaylistItem).Updated += len(slots)
	}
	return true, nil
}

// ── follows ──

// resyncRemovedFollows drops subscriptions the source no longer has. It removes
// ONLY pairs the ledger records the import having created: a subscription
// somebody made on this instance has no ledger row and is untouched.
func (im *Importer) resyncRemovedFollows(ctx context.Context, follows []SourceFollow, c *Counts) error {
	present := make(map[string]bool, len(follows))
	for _, f := range follows {
		present[fmt.Sprintf("%d:%d", f.FollowerUserID, f.ChannelID)] = true
	}
	sids := make([]string, 0, len(im.resync.follows))
	for sid := range im.resync.follows {
		sids = append(sids, sid)
	}
	sort.Strings(sids)
	for _, sid := range sids {
		if present[sid] {
			continue
		}
		sourceUser, _, ok := strings.Cut(sid, ":")
		if !ok {
			continue
		}
		follower, ok, err := im.resolveParent(ctx, KindUser, sourceUser)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		channel := im.resync.follows[sid]
		if err := im.withTx(ctx, func(q *sqlcgen.Queries) error {
			if err := q.ImportUnfollowChannel(ctx, sqlcgen.ImportUnfollowChannelParams{
				FollowerID: follower, ChannelID: channel,
			}); err != nil {
				return err
			}
			return recordLedger(ctx, q, KindFollow, sid, channel, "skipped", "the source no longer has this subscription")
		}); err != nil {
			im.markFailed(ctx, KindFollow, sid, safeErr(err))
			c.Failed++
			im.logger.WarnContext(ctx, "peertube import: follow removal failed", "source_id", sid, "error", err)
			continue
		}
		delete(im.resync.follows, sid)
		c.Updated++
	}
	return nil
}
