package peertubeimport

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Source is a READ-ONLY connection to a PeerTube PostgreSQL database. Every
// connection in the pool is pinned to read-only transactions defensively (on top
// of the least-privilege role the operator is told to use), so an accidental
// write can never touch the source. The importer only ever reads through this
// type; it is a clean-room reader of PeerTube's documented schema, not a copy of
// PeerTube code.
type Source struct {
	pool *pgxpool.Pool

	mu                sync.Mutex
	actorLinksChecked bool
	actorLinksOnActor bool
	// colCache / tblCache memoise information_schema probes. The importer never
	// assumes a source column or table exists — PeerTube renames and adds across
	// the schema range this tool accepts, and the difference between "probed and
	// absent" and "assumed present" is the difference between an import that
	// carries less and one that dies mid-run on a SQL error.
	colCache map[string]bool
	tblCache map[string]bool
}

// OpenSource dials the source PeerTube database and verifies connectivity. The
// pool forces every session read-only. The caller must Close it.
func OpenSource(ctx context.Context, dsn string) (*Source, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: parse source dsn: %w", err)
	}
	cfg.MaxConns = 4
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	// Defence in depth: pin every pooled connection to read-only so no query the
	// importer issues can ever write to the source, regardless of the role's grants.
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET default_transaction_read_only = on")
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: open source pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		// The same advice Preflight appends, because this is the dial that
		// actually fails first: the CLI opens the source before it preflights, so a
		// wrong --source-dsn never reaches the preflight that would have explained
		// it.
		return nil, fmt.Errorf("peertubeimport: source unreachable: %w%s", err, sourceDialAdvice(err))
	}
	return &Source{pool: pool}, nil
}

// NewSourceFromPool wraps an already-open pool (used by tests that seed a scratch
// PeerTube schema in a throwaway database). The caller owns the pool's lifecycle.
func NewSourceFromPool(pool *pgxpool.Pool) *Source { return &Source{pool: pool} }

// Close releases the source pool.
func (s *Source) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

// Ping verifies the source is reachable, bounded by ctx.
func (s *Source) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// DetectVersion reads PeerTube's schema version from the application table's
// migrationVersion column. It returns 0 when the table is empty/absent (an
// unverified source — a hard stop unless --force).
func (s *Source) DetectVersion(ctx context.Context) (int, error) {
	var v int
	err := s.pool.QueryRow(ctx, `SELECT "migrationVersion" FROM "application" ORDER BY id LIMIT 1`).Scan(&v)
	if err == pgx.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("peertubeimport: read source version: %w", err)
	}
	return v, nil
}

func (s *Source) hasColumn(ctx context.Context, table, column string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name = $1
			  AND column_name = $2
		)`, table, column).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("peertubeimport: inspect source schema column %s.%s: %w", table, column, err)
	}
	return exists, nil
}

// columnExists is the memoised form of hasColumn. Every optional source field
// the importer reads goes through it, so one probe per (table, column) is paid
// per run no matter how many passes ask.
func (s *Source) columnExists(ctx context.Context, table, column string) (bool, error) {
	key := table + "." + column
	s.mu.Lock()
	if found, ok := s.colCache[key]; ok {
		s.mu.Unlock()
		return found, nil
	}
	s.mu.Unlock()

	found, err := s.hasColumn(ctx, table, column)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	if s.colCache == nil {
		s.colCache = map[string]bool{}
	}
	s.colCache[key] = found
	s.mu.Unlock()
	return found, nil
}

// tableExists reports whether a source table is present at all. Whole families
// come and go across the accepted schema range — "videoChapter" does not exist
// on a PeerTube old enough to sit at the bottom of it — and a missing table is a
// family the source simply does not have, not a failure.
func (s *Source) tableExists(ctx context.Context, table string) (bool, error) {
	s.mu.Lock()
	if found, ok := s.tblCache[table]; ok {
		s.mu.Unlock()
		return found, nil
	}
	s.mu.Unlock()

	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1
		)`, table).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("peertubeimport: inspect source schema table %s: %w", table, err)
	}
	s.mu.Lock()
	if s.tblCache == nil {
		s.tblCache = map[string]bool{}
	}
	s.tblCache[table] = exists
	s.mu.Unlock()
	return exists, nil
}

// firstColumn returns the first of candidates that exists on table, or "" when
// none do. It is how a field PeerTube has spelled more than one way is read
// without the importer guessing: the spellings are tried against
// information_schema, and a source carrying none of them yields "" so the caller
// can report the family as unreadable instead of issuing SQL that cannot parse.
func (s *Source) firstColumn(ctx context.Context, table string, candidates ...string) (string, error) {
	for _, c := range candidates {
		found, err := s.columnExists(ctx, table, c)
		if err != nil {
			return "", err
		}
		if found {
			return c, nil
		}
	}
	return "", nil
}

func (s *Source) actorLinksLiveOnActor(ctx context.Context) (bool, error) {
	s.mu.Lock()
	if s.actorLinksChecked {
		onActor := s.actorLinksOnActor
		s.mu.Unlock()
		return onActor, nil
	}
	s.mu.Unlock()

	accountID, err := s.hasColumn(ctx, "actor", "accountId")
	if err != nil {
		return false, err
	}
	videoChannelID, err := s.hasColumn(ctx, "actor", "videoChannelId")
	if err != nil {
		return false, err
	}
	onActor := accountID && videoChannelID

	s.mu.Lock()
	s.actorLinksOnActor = onActor
	s.actorLinksChecked = true
	s.mu.Unlock()
	return onActor, nil
}

// ── source entity structs (Vidra-shaped, resolved from PeerTube joins) ──

// SourceUser is a LOCAL PeerTube user + its account + actor identity. Remote
// accounts (actor.serverId set) are never imported — those federate, they do not
// migrate. PasswordHash is a bcrypt hash carried as-is; it is NEVER logged.
type SourceUser struct {
	ID            int64
	Username      string
	Email         string
	PasswordHash  string
	Role          int // PeerTube UserRole: 0 admin, 1 moderator, 2 user
	EmailVerified bool
	DisplayName   string
	CreatedAt     time.Time
	PublicKeyPEM  string // account actor public key (may be empty)
	PrivateKeyPEM string // account actor private key (SECRET; may be empty)
	// Blocked is the source's user.blocked — its account SUSPENSION, and the one
	// column that says this person must not be able to sign in. It is carried
	// INVERTED into users.is_active; false when the source schema predates it.
	Blocked bool
}

// SourceChannel is a LOCAL PeerTube video channel. OwnerUserID is the source
// user id that owns it (via account.userId).
type SourceChannel struct {
	ID            int64
	OwnerUserID   int64
	Handle        string // channel actor preferredUsername
	DisplayName   string
	Description   string
	CreatedAt     time.Time
	PublicKeyPEM  string
	PrivateKeyPEM string // SECRET; may be empty
}

// SourceVideo is a LOCAL PeerTube video's metadata (files/thumbnails/captions/
// tags are fetched per video).
type SourceVideo struct {
	ID          int64
	UUID        string
	ChannelID   int64
	Title       string
	Description string
	Privacy     int // PeerTube VideoPrivacy: 1 public, 2 unlisted, 3 private, 4 internal
	State       int // PeerTube VideoState: 1 published, others = not-yet-published
	Category    *int
	Licence     *int
	Language    *string
	Duration    int
	CreatedAt   time.Time
	// Views is the source's LIFETIME view total for this video — one number, with
	// no daily breakdown behind it. 0 when the source does not carry the column.
	Views int64
	// AspectRatio is the source's recorded width/height ratio (0 when the source
	// does not record one). It is the only real evidence available for an HLS
	// rung's width when the stored file carries no dimensions.
	AspectRatio float64
	// OriginallyPublishedAt is when the video was first published somewhere
	// OTHER than the source — PeerTube's own originallyPublishedAt, filled by
	// its YouTube/instance importers or set by hand. nil for a video first
	// published on the source itself, and for every source whose schema predates
	// the column.
	OriginallyPublishedAt *time.Time
	// NSFW is the source's video.nsfw — the flag PeerTube's own hide/warn/blur
	// policy acts on, carried into videos.is_sensitive. PeerTube 7 added
	// nsfwFlags/nsfwSummary ALONGSIDE it rather than in place of it, so this one
	// boolean is still the whole answer. false when the source predates it.
	NSFW bool
}

// SourceVideoFile is one stored media file for a video (web/webseed download).
type SourceVideoFile struct {
	ID         int64
	Resolution int
	Size       int64
	Extname    string
	Filename   string
}

// SourceHLSPlaylist is one PeerTube HLS playlist for a video.
type SourceHLSPlaylist struct {
	ID               int64
	PlaylistFilename string
}

// SourceCaption is one subtitle track.
type SourceCaption struct {
	Language string
	Filename string
}

// SourceComment is a LOCAL comment (authored by a local account). ParentID is
// the source comment it replies to (0 = top-level).
type SourceComment struct {
	ID         int64
	VideoID    int64
	AuthorUser int64
	ParentID   int64
	Body       string
	CreatedAt  time.Time
}

// SourcePlaylist is a LOCAL regular playlist owned by a local account.
type SourcePlaylist struct {
	ID          int64
	OwnerUserID int64
	Title       string
	Description string
	Privacy     int // PeerTube VideoPlaylistPrivacy: 1 public, 2 unlisted, 3 private
	CreatedAt   time.Time
}

// SourcePlaylistElement is a video slot in a playlist.
type SourcePlaylistElement struct {
	Position int
	VideoID  int64
}

// SourceFollow is a LOCAL user following a LOCAL channel (a subscription).
type SourceFollow struct {
	FollowerUserID int64
	ChannelID      int64
}

// localActorFilter restricts a join to LOCAL actors (serverId IS NULL): only the
// instance's own accounts/channels are migrated, never federated remote ones.

// Users returns every local user with its account + actor identity.
func (s *Source) Users(ctx context.Context) ([]SourceUser, error) {
	onActor, err := s.actorLinksLiveOnActor(ctx)
	if err != nil {
		return nil, err
	}
	actorJoin := `act.id = acc."actorId"`
	if onActor {
		actorJoin = `act."accountId" = acc.id`
	}
	// u.password is NULLABLE on the source (PeerTube declares it
	// @AllowNull(true)): an LDAP/OIDC/SAML plugin-auth user has no locally
	// stored password. COALESCE it to '' — an empty string is not a valid
	// bcrypt hash and can never verify, so such a user imports safely locked
	// out of password login (the same property the OAuth and ATProto login
	// paths already rely on) instead of failing the scan and aborting the run.
	// blocked is probed, not assumed, like every other optional column here: a
	// source too old to carry it loses the suspension signal and nothing else.
	// false is the right absent value — it is PeerTube's own @Default(false).
	blockedExpr := `false`
	if has, err := s.columnExists(ctx, "user", "blocked"); err != nil {
		return nil, err
	} else if has {
		blockedExpr = `COALESCE(u.blocked, false)`
	}
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, u.username, u.email, COALESCE(u.password, ''),
		       u.role, COALESCE(u."emailVerified", false),
		       COALESCE(acc.name, u.username), u."createdAt",
		       COALESCE(act."publicKey", ''), COALESCE(act."privateKey", ''),
		       `+blockedExpr+`
		FROM "user" u
		JOIN account acc ON acc."userId" = u.id
		JOIN actor act ON `+actorJoin+`
		WHERE act."serverId" IS NULL
		ORDER BY u.id`)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: read users: %w", err)
	}
	defer rows.Close()
	var out []SourceUser
	for rows.Next() {
		var u SourceUser
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role,
			&u.EmailVerified, &u.DisplayName, &u.CreatedAt, &u.PublicKeyPEM, &u.PrivateKeyPEM,
			&u.Blocked); err != nil {
			return nil, fmt.Errorf("peertubeimport: scan user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Channels returns every local video channel with its owning user resolved.
func (s *Source) Channels(ctx context.Context) ([]SourceChannel, error) {
	onActor, err := s.actorLinksLiveOnActor(ctx)
	if err != nil {
		return nil, err
	}
	actorJoin := `cact.id = vc."actorId"`
	if onActor {
		actorJoin = `cact."videoChannelId" = vc.id`
	}
	rows, err := s.pool.Query(ctx, `
		SELECT vc.id, acc."userId", cact."preferredUsername", vc.name,
		       COALESCE(vc.description, ''), vc."createdAt",
		       COALESCE(cact."publicKey", ''), COALESCE(cact."privateKey", '')
		FROM "videoChannel" vc
		JOIN account acc ON acc.id = vc."accountId"
		JOIN actor cact ON `+actorJoin+`
		WHERE cact."serverId" IS NULL AND acc."userId" IS NOT NULL
		ORDER BY vc.id`)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: read channels: %w", err)
	}
	defer rows.Close()
	var out []SourceChannel
	for rows.Next() {
		var c SourceChannel
		if err := rows.Scan(&c.ID, &c.OwnerUserID, &c.Handle, &c.DisplayName,
			&c.Description, &c.CreatedAt, &c.PublicKeyPEM, &c.PrivateKeyPEM); err != nil {
			return nil, fmt.Errorf("peertubeimport: scan channel: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Videos returns every local video's metadata.
func (s *Source) Videos(ctx context.Context) ([]SourceVideo, error) {
	onActor, err := s.actorLinksLiveOnActor(ctx)
	if err != nil {
		return nil, err
	}
	actorJoin := `act.id = vc."actorId"`
	if onActor {
		actorJoin = `act."videoChannelId" = vc.id`
	}
	// views is probed rather than assumed: it is the one column here whose absence
	// should cost the view totals and nothing else, not the whole video pass.
	viewsExpr := `0::bigint`
	if hasViews, err := s.columnExists(ctx, "video", "views"); err != nil {
		return nil, err
	} else if hasViews {
		viewsExpr = `COALESCE(v.views, 0)::bigint`
	}
	aspectExpr := `0::double precision`
	if hasAspect, err := s.columnExists(ctx, "video", "aspectRatio"); err != nil {
		return nil, err
	} else if hasAspect {
		aspectExpr = `COALESCE(v."aspectRatio", 0)::double precision`
	}
	// Same probe, same reason: originallyPublishedAt is not on every schema in
	// the accepted range, and a source that predates it should lose the original
	// dates and nothing else. NULL is also what the column itself holds for a
	// video first published on the source, so the absent-column case needs no
	// separate handling downstream.
	origPubExpr := `NULL::timestamptz`
	if hasOrigPub, err := s.columnExists(ctx, "video", "originallyPublishedAt"); err != nil {
		return nil, err
	} else if hasOrigPub {
		origPubExpr = `v."originallyPublishedAt"`
	}
	// Same probe, same reason. false is PeerTube's own default for the column, so
	// a source without it flags nothing rather than flagging everything.
	nsfwExpr := `false`
	if hasNSFW, err := s.columnExists(ctx, "video", "nsfw"); err != nil {
		return nil, err
	} else if hasNSFW {
		nsfwExpr = `COALESCE(v.nsfw, false)`
	}
	rows, err := s.pool.Query(ctx, `
		SELECT v.id, v.uuid::text, v."channelId", v.name, COALESCE(v.description, ''),
		       v.privacy, v.state, v.category, v.licence, v.language, v.duration, v."createdAt",
		       `+viewsExpr+`, `+aspectExpr+`, `+origPubExpr+`, `+nsfwExpr+`
		FROM video v
		JOIN "videoChannel" vc ON vc.id = v."channelId"
		JOIN actor act ON `+actorJoin+`
		WHERE act."serverId" IS NULL
		ORDER BY v.id`)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: read videos: %w", err)
	}
	defer rows.Close()
	var out []SourceVideo
	for rows.Next() {
		var v SourceVideo
		var uuidStr string
		if err := rows.Scan(&v.ID, &uuidStr, &v.ChannelID, &v.Title, &v.Description,
			&v.Privacy, &v.State, &v.Category, &v.Licence, &v.Language, &v.Duration, &v.CreatedAt,
			&v.Views, &v.AspectRatio, &v.OriginallyPublishedAt, &v.NSFW); err != nil {
			return nil, fmt.Errorf("peertubeimport: scan video: %w", err)
		}
		v.UUID = uuidStr
		out = append(out, v)
	}
	return out, rows.Err()
}

// VideoFiles returns a video's web (non-HLS) media files, highest resolution
// first. The importer copies the highest-resolution file as Vidra's original.
func (s *Source) VideoFiles(ctx context.Context, videoID int64) ([]SourceVideoFile, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, resolution, size, COALESCE(extname, ''), COALESCE(filename, '')
		FROM "videoFile"
		WHERE "videoId" = $1
		ORDER BY resolution DESC, id`, videoID)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: read video files: %w", err)
	}
	defer rows.Close()
	var out []SourceVideoFile
	for rows.Next() {
		var f SourceVideoFile
		if err := rows.Scan(&f.ID, &f.Resolution, &f.Size, &f.Extname, &f.Filename); err != nil {
			return nil, fmt.Errorf("peertubeimport: scan video file: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// HLSPlaylist returns the first HLS streaming playlist for a video, when present.
func (s *Source) HLSPlaylist(ctx context.Context, videoID int64) (SourceHLSPlaylist, bool, error) {
	var p SourceHLSPlaylist
	err := s.pool.QueryRow(ctx, `
		SELECT id, COALESCE("playlistFilename", '')
		FROM "videoStreamingPlaylist"
		WHERE "videoId" = $1
		ORDER BY id LIMIT 1`, videoID).Scan(&p.ID, &p.PlaylistFilename)
	if err == pgx.ErrNoRows {
		return SourceHLSPlaylist{}, false, nil
	}
	if err != nil {
		return SourceHLSPlaylist{}, false, fmt.Errorf("peertubeimport: read hls playlist: %w", err)
	}
	if p.PlaylistFilename == "" {
		return SourceHLSPlaylist{}, false, nil
	}
	return p, true, nil
}

// SourceVideoThumbnail is the ONE thumbnail row chosen for a local video, with
// the pixel size the source recorded for it (0 when it records none).
type SourceVideoThumbnail struct {
	ID       int64
	VideoID  int64
	Filename string
	Width    int
	Height   int
}

// VideoThumbnails returns one thumbnail row per LOCAL video — the best poster
// the source offers for it. ok=false means the source has no thumbnail table at
// all, which is a family the source does not have rather than a failure.
//
// ── why there is a choice to make, and how it is made ──
//
// PeerTube 8.1 unified previews and miniatures into one table and started
// writing ONE ROW PER CONFIGURED SIZE: the shipped set is 280x157, 850x480,
// 1280x720, 1920x1080 and a 1400x1400 square "for podcast applications", and an
// admin can change it. They all describe the same video, so exactly one of them
// can be this video's poster here, and the older read ("ORDER BY id LIMIT 1",
// with a type filter that is inert on 8.1+) took whichever the source happened
// to insert first — on a multi-size source, the 280x157 thumbnail. That is the
// same defect that left 137 avatars as thumbnails, in the same shape, so this is
// the same fix: DISTINCT ON the video, and let the ORDER BY be the choice.
//
// The order is, in priority:
//
//  1. On a PRE-8.1 source, which still has `type`, a PREVIEW (2) beats a
//     MINIATURE (1). Both exist there and the preview is the full-size one.
//  2. A NON-SQUARE row beats a square one. Vidra renders one poster into 16:9
//     card and watch-page surfaces, and PeerTube's 1400x1400 podcast variant has
//     MORE PIXELS than its 1280x720 — so a naive largest-first rule picks the
//     square and every card in the catalogue gets a letterboxed poster.
//  3. Then the largest by area, and then the newest id, so the answer is
//     deterministic even on a source that records no sizes at all.
//
// Every optional column is probed rather than assumed: `type` was DROPPED in 8.1
// and a source that lacks it must still be readable, and a column that is not
// there is a syntax error rather than a NULL.
//
// The same table also holds PLAYLIST thumbnails (videoPlaylistId set, videoId
// null), which are not this family; the filter excludes them.
func (s *Source) VideoThumbnails(ctx context.Context) ([]SourceVideoThumbnail, bool, error) {
	present, err := s.tableExists(ctx, "thumbnail")
	if err != nil || !present {
		return nil, false, err
	}
	hasType, err := s.columnExists(ctx, "thumbnail", "type")
	if err != nil {
		return nil, false, err
	}
	hasWidth, err := s.columnExists(ctx, "thumbnail", "width")
	if err != nil {
		return nil, false, err
	}
	hasHeight, err := s.columnExists(ctx, "thumbnail", "height")
	if err != nil {
		return nil, false, err
	}
	widthExpr, heightExpr := `0`, `0`
	if hasWidth {
		widthExpr = `COALESCE(t.width, 0)`
	}
	if hasHeight {
		heightExpr = `COALESCE(t.height, 0)`
	}
	var order string
	if hasType {
		order += `CASE WHEN t.type = 2 THEN 0 ELSE 1 END, `
	}
	if hasWidth && hasHeight {
		// A square is sorted LAST, and only when the source actually recorded a
		// size — with no sizes recorded every row reads 0x0 and this is inert.
		order += `CASE WHEN ` + widthExpr + ` > 0 AND ` + widthExpr + ` = ` + heightExpr + ` THEN 1 ELSE 0 END, `
		order += `(` + widthExpr + ` * ` + heightExpr + `) DESC, `
	} else if hasWidth {
		// DESC alone would sort NULLs FIRST in PostgreSQL, i.e. a row whose size
		// the source never recorded would beat every row whose size it did.
		order += `t.width DESC NULLS LAST, `
	} else if hasHeight {
		order += `t.height DESC NULLS LAST, `
	}
	onActor, err := s.actorLinksLiveOnActor(ctx)
	if err != nil {
		return nil, false, err
	}
	actorJoin := `act.id = vc."actorId"`
	if onActor {
		actorJoin = `act."videoChannelId" = vc.id`
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, video_id, filename, width, height
		FROM (
			SELECT DISTINCT ON (t."videoId")
			       t.id                     AS id,
			       t."videoId"              AS video_id,
			       COALESCE(t.filename, '') AS filename,
			       `+widthExpr+`            AS width,
			       `+heightExpr+`           AS height
			FROM thumbnail t
			JOIN video v ON v.id = t."videoId"
			JOIN "videoChannel" vc ON vc.id = v."channelId"
			JOIN actor act ON `+actorJoin+`
			WHERE t."videoId" IS NOT NULL AND act."serverId" IS NULL
			ORDER BY t."videoId", `+order+`t.id DESC
		) best
		ORDER BY best.video_id`)
	if err != nil {
		return nil, false, fmt.Errorf("peertubeimport: read video thumbnails: %w", err)
	}
	defer rows.Close()
	var out []SourceVideoThumbnail
	for rows.Next() {
		var th SourceVideoThumbnail
		if err := rows.Scan(&th.ID, &th.VideoID, &th.Filename, &th.Width, &th.Height); err != nil {
			return nil, false, fmt.Errorf("peertubeimport: scan video thumbnail: %w", err)
		}
		out = append(out, th)
	}
	return out, true, rows.Err()
}

// SourceStoryboard is one video's seek-preview sprite sheet on the source: the
// stored file plus the geometry needed to describe it, since PeerTube stores the
// sheet and no WebVTT map to go with it.
type SourceStoryboard struct {
	ID          int64
	VideoID     int64
	Filename    string
	TotalWidth  int
	TotalHeight int
	SpriteWidth int
	// SpriteHeight and SpriteDuration complete the grid: how tall one tile is,
	// and how many SECONDS of video it spans.
	SpriteHeight   int
	SpriteDuration int
}

// Storyboards returns every LOCAL video's storyboard row. ok=false means the
// source has no storyboard table, which is every PeerTube older than 6.0 and is
// a family the source does not have rather than a failure.
//
// The table MUST be probed and cannot be inferred from the schema version:
// PeerTube never wrote a migration for it — it is created by
// sequelizeTypescript.sync() on boot — so migrationVersion says nothing at all
// about whether it is there.
//
// Expect legitimate absences even on a source that has the table: PeerTube's
// generation job returns early for a video shorter than three seconds and writes
// no row for it.
func (s *Source) Storyboards(ctx context.Context) ([]SourceStoryboard, bool, error) {
	present, err := s.tableExists(ctx, "storyboard")
	if err != nil || !present {
		return nil, false, err
	}
	// The geometry columns have been there since the table was, but the table is
	// schema-synced rather than migrated, so nothing about it is guaranteed. A
	// column that is missing reads 0 and the row is reported unsupported by the
	// pass, which is a far better outcome than SQL that cannot parse.
	geom := map[string]string{}
	for _, col := range []string{"totalWidth", "totalHeight", "spriteWidth", "spriteHeight", "spriteDuration"} {
		has, err := s.columnExists(ctx, "storyboard", col)
		if err != nil {
			return nil, false, err
		}
		if has {
			geom[col] = `COALESCE(sb."` + col + `", 0)`
		} else {
			geom[col] = `0`
		}
	}
	onActor, err := s.actorLinksLiveOnActor(ctx)
	if err != nil {
		return nil, false, err
	}
	actorJoin := `act.id = vc."actorId"`
	if onActor {
		actorJoin = `act."videoChannelId" = vc.id`
	}
	rows, err := s.pool.Query(ctx, `
		SELECT sb.id, sb."videoId", COALESCE(sb.filename, ''),
		       `+geom["totalWidth"]+`, `+geom["totalHeight"]+`,
		       `+geom["spriteWidth"]+`, `+geom["spriteHeight"]+`,
		       `+geom["spriteDuration"]+`
		FROM storyboard sb
		JOIN video v ON v.id = sb."videoId"
		JOIN "videoChannel" vc ON vc.id = v."channelId"
		JOIN actor act ON `+actorJoin+`
		WHERE act."serverId" IS NULL
		ORDER BY sb."videoId", sb.id`)
	if err != nil {
		return nil, false, fmt.Errorf("peertubeimport: read storyboards: %w", err)
	}
	defer rows.Close()
	var out []SourceStoryboard
	for rows.Next() {
		var sb SourceStoryboard
		if err := rows.Scan(&sb.ID, &sb.VideoID, &sb.Filename, &sb.TotalWidth, &sb.TotalHeight,
			&sb.SpriteWidth, &sb.SpriteHeight, &sb.SpriteDuration); err != nil {
			return nil, false, fmt.Errorf("peertubeimport: scan storyboard: %w", err)
		}
		out = append(out, sb)
	}
	return out, true, rows.Err()
}

// Captions returns a video's caption tracks.
func (s *Source) Captions(ctx context.Context, videoID int64) ([]SourceCaption, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT language, COALESCE(filename, '')
		FROM "videoCaption"
		WHERE "videoId" = $1
		ORDER BY language`, videoID)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: read captions: %w", err)
	}
	defer rows.Close()
	var out []SourceCaption
	for rows.Next() {
		var c SourceCaption
		if err := rows.Scan(&c.Language, &c.Filename); err != nil {
			return nil, fmt.Errorf("peertubeimport: scan caption: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Tags returns a video's tag names.
func (s *Source) Tags(ctx context.Context, videoID int64) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.name FROM "videoTag" vt
		JOIN tag t ON t.id = vt."tagId"
		WHERE vt."videoId" = $1
		ORDER BY t.name`, videoID)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: read tags: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("peertubeimport: scan tag: %w", err)
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// AllTags returns every LOCAL video's tags, keyed by the video's numeric source
// id. It is the bulk form of Tags, and it exists for the same reason Chapters
// and Ratings are read in bulk: a source-authoritative re-run has to know every
// video's tag set to notice one that changed, and asking per video is 14,766
// round trips over an SSH tunnel — enough on its own to turn a 21-second no-op
// re-run into minutes. Tags stay per-video in the INSERT path, where they are
// only read for the handful of videos a run actually imports.
func (s *Source) AllTags(ctx context.Context) (map[int64][]string, error) {
	onActor, err := s.actorLinksLiveOnActor(ctx)
	if err != nil {
		return nil, err
	}
	actorJoin := `act.id = vc."actorId"`
	if onActor {
		actorJoin = `act."videoChannelId" = vc.id`
	}
	rows, err := s.pool.Query(ctx, `
		SELECT vt."videoId", t.name
		FROM "videoTag" vt
		JOIN tag t ON t.id = vt."tagId"
		JOIN video v ON v.id = vt."videoId"
		JOIN "videoChannel" vc ON vc.id = v."channelId"
		JOIN actor act ON `+actorJoin+`
		WHERE act."serverId" IS NULL
		ORDER BY vt."videoId", t.name`)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: read all tags: %w", err)
	}
	defer rows.Close()
	out := map[int64][]string{}
	for rows.Next() {
		var videoID int64
		var name string
		if err := rows.Scan(&videoID, &name); err != nil {
			return nil, fmt.Errorf("peertubeimport: scan tag: %w", err)
		}
		out[videoID] = append(out[videoID], name)
	}
	return out, rows.Err()
}

// AllPlaylistElements returns every LOCAL regular playlist's slots, keyed by the
// playlist's numeric source id and in playback order. Bulk for the same reason
// AllTags is: a resync compares every playlist's whole item list, and the
// per-playlist read would be one round trip each.
func (s *Source) AllPlaylistElements(ctx context.Context) (map[int64][]SourcePlaylistElement, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e."videoPlaylistId", e.position, e."videoId"
		FROM "videoPlaylistElement" e
		JOIN "videoPlaylist" vp ON vp.id = e."videoPlaylistId"
		JOIN account acc ON acc.id = vp."ownerAccountId"
		WHERE e."videoId" IS NOT NULL AND acc."userId" IS NOT NULL AND vp.type = 1
		ORDER BY e."videoPlaylistId", e.position`)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: read all playlist elements: %w", err)
	}
	defer rows.Close()
	out := map[int64][]SourcePlaylistElement{}
	for rows.Next() {
		var playlistID int64
		var e SourcePlaylistElement
		if err := rows.Scan(&playlistID, &e.Position, &e.VideoID); err != nil {
			return nil, fmt.Errorf("peertubeimport: scan playlist element: %w", err)
		}
		out[playlistID] = append(out[playlistID], e)
	}
	return out, rows.Err()
}

// Comments returns every local (locally-authored, non-deleted) comment, parents
// before replies (ORDER BY id), so the importer can resolve parent_id from the
// ledger as it goes.
func (s *Source) Comments(ctx context.Context) ([]SourceComment, error) {
	onActor, err := s.actorLinksLiveOnActor(ctx)
	if err != nil {
		return nil, err
	}
	actorJoin := `act.id = acc."actorId"`
	if onActor {
		actorJoin = `act."accountId" = acc.id`
	}
	rows, err := s.pool.Query(ctx, `
		SELECT vc.id, vc."videoId", acc."userId",
		       COALESCE(vc."inReplyToCommentId", 0), vc.text, vc."createdAt"
		FROM "videoComment" vc
		JOIN account acc ON acc.id = vc."accountId"
		JOIN actor act ON `+actorJoin+`
		WHERE vc."deletedAt" IS NULL AND act."serverId" IS NULL AND acc."userId" IS NOT NULL
		ORDER BY vc.id`)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: read comments: %w", err)
	}
	defer rows.Close()
	var out []SourceComment
	for rows.Next() {
		var c SourceComment
		if err := rows.Scan(&c.ID, &c.VideoID, &c.AuthorUser, &c.ParentID, &c.Body, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("peertubeimport: scan comment: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Playlists returns every local regular playlist (type 1) owned by a local user.
func (s *Source) Playlists(ctx context.Context) ([]SourcePlaylist, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT vp.id, acc."userId", vp.name, COALESCE(vp.description, ''),
		       vp.privacy, vp."createdAt"
		FROM "videoPlaylist" vp
		JOIN account acc ON acc.id = vp."ownerAccountId"
		WHERE acc."userId" IS NOT NULL AND vp.type = 1
		ORDER BY vp.id`)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: read playlists: %w", err)
	}
	defer rows.Close()
	var out []SourcePlaylist
	for rows.Next() {
		var p SourcePlaylist
		if err := rows.Scan(&p.ID, &p.OwnerUserID, &p.Title, &p.Description, &p.Privacy, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("peertubeimport: scan playlist: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PlaylistElements returns a playlist's video slots in order (skipping deleted
// videos, whose videoId is null).
func (s *Source) PlaylistElements(ctx context.Context, playlistID int64) ([]SourcePlaylistElement, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT position, "videoId"
		FROM "videoPlaylistElement"
		WHERE "videoPlaylistId" = $1 AND "videoId" IS NOT NULL
		ORDER BY position`, playlistID)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: read playlist elements: %w", err)
	}
	defer rows.Close()
	var out []SourcePlaylistElement
	for rows.Next() {
		var e SourcePlaylistElement
		if err := rows.Scan(&e.Position, &e.VideoID); err != nil {
			return nil, fmt.Errorf("peertubeimport: scan playlist element: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Follows returns every accepted LOCAL-user → LOCAL-channel subscription.
func (s *Source) Follows(ctx context.Context) ([]SourceFollow, error) {
	onActor, err := s.actorLinksLiveOnActor(ctx)
	if err != nil {
		return nil, err
	}
	accountJoin := `facc."actorId" = fa.id`
	channelJoin := `tvc."actorId" = ta.id`
	if onActor {
		accountJoin = `facc.id = fa."accountId"`
		channelJoin = `tvc.id = ta."videoChannelId"`
	}
	rows, err := s.pool.Query(ctx, `
		SELECT facc."userId", tvc.id
		FROM "actorFollow" af
		JOIN actor fa ON fa.id = af."actorId"
		JOIN account facc ON `+accountJoin+`
		JOIN actor ta ON ta.id = af."targetActorId"
		JOIN "videoChannel" tvc ON `+channelJoin+`
		WHERE af.state = 'accepted'
		  AND fa."serverId" IS NULL AND ta."serverId" IS NULL
		  AND facc."userId" IS NOT NULL
		ORDER BY facc."userId", tvc.id`)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: read follows: %w", err)
	}
	defer rows.Close()
	var out []SourceFollow
	for rows.Next() {
		var f SourceFollow
		if err := rows.Scan(&f.FollowerUserID, &f.ChannelID); err != nil {
			return nil, fmt.Errorf("peertubeimport: scan follow: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ── per-video data read in bulk (chapters, ratings, HLS rungs) ──
//
// These three are read with ONE query each for the whole instance rather than
// one per video. The per-video shape the older families use costs a round trip
// per video, which on a real catalogue (14,766 videos) turns a 20-second re-run
// into minutes over an SSH tunnel — and these families are exactly the ones an
// operator re-runs on a schedule. Each is ordered by video so the caller walks
// them alongside its own per-video state.

// SourceChapter is one seek-bar chapter mark on a video.
type SourceChapter struct {
	ID       int64
	VideoID  int64
	Timecode int // seconds from the start
	Title    string
}

// Chapters returns every chapter on every LOCAL video, oldest video first and
// in playback order within a video. ok=false means the source has no chapter
// table at all (PeerTube grew chapters part-way through the accepted schema
// range), which is a family the source does not have — not a failure.
func (s *Source) Chapters(ctx context.Context) ([]SourceChapter, bool, error) {
	present, err := s.tableExists(ctx, "videoChapter")
	if err != nil {
		return nil, false, err
	}
	if !present {
		return nil, false, nil
	}
	// The start-offset column: PeerTube calls it "timecode". The alternatives are
	// tried in case an older/newer schema in the accepted range spells it
	// differently; none of them found means the table is not one we can read.
	timecodeCol, err := s.firstColumn(ctx, "videoChapter", "timecode", "startTimecode", "start")
	if err != nil {
		return nil, false, err
	}
	if timecodeCol == "" {
		return nil, false, fmt.Errorf(`peertubeimport: source "videoChapter" has no recognised start-offset column`)
	}
	onActor, err := s.actorLinksLiveOnActor(ctx)
	if err != nil {
		return nil, false, err
	}
	actorJoin := `act.id = vc."actorId"`
	if onActor {
		actorJoin = `act."videoChannelId" = vc.id`
	}
	rows, err := s.pool.Query(ctx, `
		SELECT ch.id, ch."videoId", ch."`+timecodeCol+`", COALESCE(ch.title, '')
		FROM "videoChapter" ch
		JOIN video v ON v.id = ch."videoId"
		JOIN "videoChannel" vc ON vc.id = v."channelId"
		JOIN actor act ON `+actorJoin+`
		WHERE act."serverId" IS NULL
		ORDER BY ch."videoId", ch."`+timecodeCol+`"`)
	if err != nil {
		return nil, false, fmt.Errorf("peertubeimport: read chapters: %w", err)
	}
	defer rows.Close()
	var out []SourceChapter
	for rows.Next() {
		var c SourceChapter
		if err := rows.Scan(&c.ID, &c.VideoID, &c.Timecode, &c.Title); err != nil {
			return nil, false, fmt.Errorf("peertubeimport: scan chapter: %w", err)
		}
		out = append(out, c)
	}
	return out, true, rows.Err()
}

// SourceRating is one LOCAL user's like/dislike of a video. Type is the source's
// own spelling ('like' / 'dislike'); the caller validates it.
type SourceRating struct {
	ID        int64
	VideoID   int64
	RaterUser int64
	Type      string
	CreatedAt time.Time
}

// Ratings returns every rating cast by a LOCAL user on a LOCAL video. Remote
// accounts' ratings are excluded for the same reason remote comments are: there
// is no Vidra user to attribute them to, and video_ratings is keyed by one.
func (s *Source) Ratings(ctx context.Context) ([]SourceRating, bool, error) {
	present, err := s.tableExists(ctx, "accountVideoRate")
	if err != nil {
		return nil, false, err
	}
	if !present {
		return nil, false, nil
	}
	onActor, err := s.actorLinksLiveOnActor(ctx)
	if err != nil {
		return nil, false, err
	}
	accountJoin := `act.id = acc."actorId"`
	if onActor {
		accountJoin = `act."accountId" = acc.id`
	}
	channelJoin := `vact.id = vc."actorId"`
	if onActor {
		channelJoin = `vact."videoChannelId" = vc.id`
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r."videoId", acc."userId", r.type, r."createdAt"
		FROM "accountVideoRate" r
		JOIN account acc ON acc.id = r."accountId"
		JOIN actor act ON `+accountJoin+`
		JOIN video v ON v.id = r."videoId"
		JOIN "videoChannel" vc ON vc.id = v."channelId"
		JOIN actor vact ON `+channelJoin+`
		WHERE act."serverId" IS NULL AND vact."serverId" IS NULL
		  AND acc."userId" IS NOT NULL
		ORDER BY r."videoId", r.id`)
	if err != nil {
		return nil, false, fmt.Errorf("peertubeimport: read ratings: %w", err)
	}
	defer rows.Close()
	var out []SourceRating
	for rows.Next() {
		var r SourceRating
		if err := rows.Scan(&r.ID, &r.VideoID, &r.RaterUser, &r.Type, &r.CreatedAt); err != nil {
			return nil, false, fmt.Errorf("peertubeimport: scan rating: %w", err)
		}
		out = append(out, r)
	}
	return out, true, rows.Err()
}

// ── actor images (account + channel avatars and banners) ──

// SourceActorImage is one row of the source's actorImage table, with the local
// account or channel it belongs to already resolved. Exactly one of UserID /
// ChannelID is set for a row this importer can use; both nil means the actor is
// neither a local account with a user nor a local channel (the instance's own
// system actor, or an account whose user row is gone).
type SourceActorImage struct {
	ID       int64
	Filename string
	// Type is PeerTube's ActorImageType: 1 = avatar, 2 = banner. Sources too old
	// to carry the column report 1, because before banners existed every row was
	// an avatar.
	Type      int
	UserID    *int64
	ChannelID *int64
	// Width and Height are the pixel size the source recorded for this variant, 0
	// when it records none. They are carried so the ledger note can say WHICH
	// variant was chosen — see ActorImages for why there is a choice to make.
	Width  int
	Height int
}

// ActorImages returns ONE actorImage row per local actor per image type — the
// LARGEST variant the source offers for that slot. ok=false means the source has
// no actorImage table at all (PeerTube kept avatars in a separate `avatar` table
// before it), which is a family the source does not have rather than a failure.
//
// The LOCAL filter is what makes this pass small: a federated instance's
// actorImage table is mostly rows for REMOTE actors it has cached (on the
// migration this was written for, 12,893 rows down to the ~1,362 files that
// actually exist on disk). Remote actors are not imported, so their images are
// not either.
//
// ── why this deduplicates, and why "largest" ──
//
// PeerTube stores SEVERAL resolutions of every avatar — it generates a set of
// square thumbnails from one upload and keeps a row per size. Every one of those
// rows names the same slot on this side (a user has one avatar), and the
// destination key is derived from the Vidra id, so they all resolve to the SAME
// object key and overwrite each other. The pass runs at concurrency 4, so which
// one survived was a race.
//
// It was not a theoretical race. On a real migration this returned 1,316 rows
// for 309 distinct actors and 137 of the 229 imported user avatars ended up
// under 5 KB — thumbnails — while the largest variant available was 2.1 MB.
// Picking the biggest row per slot is what makes the outcome both correct and
// deterministic; even where the source records no sizes, collapsing to one row
// per slot is what removes the race.
func (s *Source) ActorImages(ctx context.Context) ([]SourceActorImage, bool, error) {
	present, err := s.tableExists(ctx, "actorImage")
	if err != nil {
		return nil, false, err
	}
	if !present {
		return nil, false, nil
	}
	// The avatar/banner discriminator arrived with banners. A source without it
	// only ever had avatars, so every row reads as type 1 rather than as an
	// unreadable family — and the slot key is then the actor alone, because an
	// actor could not have had two kinds of image.
	typeExpr, dedupKey := `1`, `ai."actorId"`
	if hasType, err := s.columnExists(ctx, "actorImage", "type"); err != nil {
		return nil, false, err
	} else if hasType {
		typeExpr = `COALESCE(ai.type, 1)`
		dedupKey = `ai."actorId", ` + typeExpr
	}
	// The size columns are PROBED, never assumed. PeerTube spells them width and
	// height on the schema range this tool accepts, but a column that is not there
	// is a syntax error rather than a NULL, and this whole family is optional. A
	// source that records no sizes still gets exactly one row per slot — the
	// newest — because the DEDUP is what fixes the race; preferring the largest is
	// what fixes the resolution.
	hasWidth, err := s.columnExists(ctx, "actorImage", "width")
	if err != nil {
		return nil, false, err
	}
	hasHeight, err := s.columnExists(ctx, "actorImage", "height")
	if err != nil {
		return nil, false, err
	}
	widthExpr, heightExpr, sizeOrder := `0`, `0`, ``
	if hasWidth {
		widthExpr = `COALESCE(ai.width, 0)`
		// DESC alone would sort NULLs FIRST in PostgreSQL, i.e. a row whose size
		// the source never recorded would beat every row whose size it did.
		sizeOrder += `ai.width DESC NULLS LAST, `
	}
	if hasHeight {
		heightExpr = `COALESCE(ai.height, 0)`
		sizeOrder += `ai.height DESC NULLS LAST, `
	}
	onActor, err := s.actorLinksLiveOnActor(ctx)
	if err != nil {
		return nil, false, err
	}
	accountJoin := `acc."actorId" = a.id`
	channelJoin := `vc."actorId" = a.id`
	if onActor {
		accountJoin = `acc.id = a."accountId"`
		channelJoin = `vc.id = a."videoChannelId"`
	}
	// DISTINCT ON keeps the first row of each slot group, so the ORDER BY inside
	// the subquery IS the choice: biggest first, newest id breaking every tie.
	rows, err := s.pool.Query(ctx, `
		SELECT id, filename, image_type, user_id, channel_id, width, height
		FROM (
			SELECT DISTINCT ON (`+dedupKey+`)
			       ai.id                     AS id,
			       COALESCE(ai.filename, '') AS filename,
			       `+typeExpr+`              AS image_type,
			       acc."userId"              AS user_id,
			       vc.id                     AS channel_id,
			       `+widthExpr+`             AS width,
			       `+heightExpr+`            AS height
			FROM "actorImage" ai
			JOIN actor a ON a.id = ai."actorId"
			LEFT JOIN account acc ON `+accountJoin+`
			LEFT JOIN "videoChannel" vc ON `+channelJoin+`
			WHERE a."serverId" IS NULL
			ORDER BY `+dedupKey+`, `+sizeOrder+`ai.id DESC
		) best
		ORDER BY best.id`)
	if err != nil {
		return nil, false, fmt.Errorf("peertubeimport: read actor images: %w", err)
	}
	defer rows.Close()
	var out []SourceActorImage
	for rows.Next() {
		var img SourceActorImage
		if err := rows.Scan(&img.ID, &img.Filename, &img.Type, &img.UserID, &img.ChannelID, &img.Width, &img.Height); err != nil {
			return nil, false, fmt.Errorf("peertubeimport: scan actor image: %w", err)
		}
		out = append(out, img)
	}
	return out, true, rows.Err()
}

// LocalActorURLs returns the canonical URLs of the source's own actors
// (https://host/accounts/<name>, https://host/video-channels/<name>). It is how
// the importer learns the source's PUBLIC ORIGIN without asking the operator for
// it — see fetchActorImage for why an origin is needed at all. Bounded, because
// only the origin is wanted and every local actor carries the same one; the
// caller takes the majority so one stale row cannot misdirect the whole run.
func (s *Source) LocalActorURLs(ctx context.Context) ([]string, error) {
	has, err := s.columnExists(ctx, "actor", "url")
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT url FROM actor
		WHERE "serverId" IS NULL AND url LIKE 'http%'
		ORDER BY id
		LIMIT 64`)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: read actor urls: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, fmt.Errorf("peertubeimport: scan actor url: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SourceRendition is one rung of a video's HLS ladder on the source: a stored
// media file attached to a streaming playlist rather than to the video directly.
// Width is what the source recorded when it records one at all — see
// renditionWidth for what happens when it does not.
type SourceRendition struct {
	FileID     int64
	VideoID    int64
	VideoUUID  string
	Resolution int // PeerTube's rung height in pixels; 0 means audio-only
	Size       int64
	Width      int // 0 when the source records no width
	Height     int // 0 when the source records no height
}

// Renditions returns every HLS ladder rung of every LOCAL video, tallest first
// within a video. These are the rungs the source's own master playlist
// advertises, which is why an imported video can play a quality ladder while
// reporting no renditions: the manifest has them, the database did not.
func (s *Source) Renditions(ctx context.Context) ([]SourceRendition, error) {
	onActor, err := s.actorLinksLiveOnActor(ctx)
	if err != nil {
		return nil, err
	}
	actorJoin := `act.id = vc."actorId"`
	if onActor {
		actorJoin = `act."videoChannelId" = vc.id`
	}
	// Pixel dimensions per stored file are a newer addition; probed, not assumed.
	widthExpr, heightExpr := `0`, `0`
	if has, err := s.columnExists(ctx, "videoFile", "width"); err != nil {
		return nil, err
	} else if has {
		widthExpr = `COALESCE(f.width, 0)`
	}
	if has, err := s.columnExists(ctx, "videoFile", "height"); err != nil {
		return nil, err
	} else if has {
		heightExpr = `COALESCE(f.height, 0)`
	}
	rows, err := s.pool.Query(ctx, `
		SELECT f.id, vsp."videoId", v.uuid::text, f.resolution, f.size,
		       `+widthExpr+`, `+heightExpr+`
		FROM "videoFile" f
		JOIN "videoStreamingPlaylist" vsp ON vsp.id = f."videoStreamingPlaylistId"
		JOIN video v ON v.id = vsp."videoId"
		JOIN "videoChannel" vc ON vc.id = v."channelId"
		JOIN actor act ON `+actorJoin+`
		WHERE act."serverId" IS NULL
		ORDER BY vsp."videoId", f.resolution DESC, f.id`)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: read renditions: %w", err)
	}
	defer rows.Close()
	var out []SourceRendition
	for rows.Next() {
		var r SourceRendition
		if err := rows.Scan(&r.FileID, &r.VideoID, &r.VideoUUID, &r.Resolution, &r.Size,
			&r.Width, &r.Height); err != nil {
			return nil, fmt.Errorf("peertubeimport: scan rendition: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── the instance's own category taxonomy ──

// SourceCategory is one category the source's categories plugin defines: a
// numeric id (PeerTube's own, which is what makes an imported video's category
// id still mean something) and the label the instance shows for it.
type SourceCategory struct {
	ID    int
	Label string
}

// SourceCategoryTaxonomy is the source instance's replacement taxonomy as the
// plugin records it: Add defines the instance's own categories, Delete lists the
// stock ids it has withdrawn. Both are needed to arrive at what the instance
// actually offers — an instance that deletes all eighteen stock entries and adds
// its own is the case this exists for.
type SourceCategoryTaxonomy struct {
	Add    []SourceCategory
	Delete []int
}

// CategoryTaxonomy reads the source's custom category taxonomy out of the
// plugin settings. ok=false means this source does not define one — no plugin
// table, no categories plugin row, an installed-but-disabled one, or a row whose
// settings carry no taxonomy — and in every one of those cases the built-in
// taxonomy stands. That is the common case: most PeerTube instances run the
// stock list, and an import must not write an override for them.
//
// Nothing about the schema is assumed. The plugin table itself and each column
// the filter uses are probed through information_schema first: a source without
// them is a source that has no plugin taxonomy, not a failed run.
func (s *Source) CategoryTaxonomy(ctx context.Context) (SourceCategoryTaxonomy, bool, error) {
	present, err := s.tableExists(ctx, "plugin")
	if err != nil || !present {
		return SourceCategoryTaxonomy{}, false, err
	}
	hasSettings, err := s.columnExists(ctx, "plugin", "settings")
	if err != nil || !hasSettings {
		return SourceCategoryTaxonomy{}, false, err
	}
	// An installed-but-disabled (or uninstalled) plugin is not defining the
	// instance's taxonomy — PeerTube stops applying it, so neither do we.
	where := `name IN ('categories', 'peertube-plugin-categories')`
	if has, err := s.columnExists(ctx, "plugin", "enabled"); err != nil {
		return SourceCategoryTaxonomy{}, false, err
	} else if has {
		where += ` AND COALESCE(enabled, true) = true`
	}
	if has, err := s.columnExists(ctx, "plugin", "uninstalled"); err != nil {
		return SourceCategoryTaxonomy{}, false, err
	} else if has {
		where += ` AND COALESCE(uninstalled, false) = false`
	}
	// settings::text, not a typed scan: the column is jsonb on the schemas this
	// tool accepts, and the value inside is itself JSON-encoded a second time
	// (see parsePluginCategories), so it is decoded here, once, from its text form.
	var raw string
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(settings::text, '')
		FROM plugin
		WHERE `+where+`
		ORDER BY id
		LIMIT 1`).Scan(&raw)
	if err == pgx.ErrNoRows {
		return SourceCategoryTaxonomy{}, false, nil
	}
	if err != nil {
		return SourceCategoryTaxonomy{}, false, fmt.Errorf("peertubeimport: read category plugin settings: %w", err)
	}
	tax, ok := parsePluginCategories(raw)
	return tax, ok, nil
}
