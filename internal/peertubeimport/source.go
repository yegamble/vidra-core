package peertubeimport

import (
	"context"
	"fmt"
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
		return nil, fmt.Errorf("peertubeimport: source unreachable: %w", err)
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
}

// SourceVideoFile is one stored media file for a video (web/webseed download).
type SourceVideoFile struct {
	ID         int64
	Resolution int
	Size       int64
	Extname    string
	Filename   string
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
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, u.username, u.email, u.password,
		       u.role, COALESCE(u."emailVerified", false),
		       COALESCE(acc.name, u.username), u."createdAt",
		       COALESCE(act."publicKey", ''), COALESCE(act."privateKey", '')
		FROM "user" u
		JOIN account acc ON acc."userId" = u.id
		JOIN actor act ON act.id = acc."actorId"
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
			&u.EmailVerified, &u.DisplayName, &u.CreatedAt, &u.PublicKeyPEM, &u.PrivateKeyPEM); err != nil {
			return nil, fmt.Errorf("peertubeimport: scan user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Channels returns every local video channel with its owning user resolved.
func (s *Source) Channels(ctx context.Context) ([]SourceChannel, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT vc.id, acc."userId", cact."preferredUsername", vc.name,
		       COALESCE(vc.description, ''), vc."createdAt",
		       COALESCE(cact."publicKey", ''), COALESCE(cact."privateKey", '')
		FROM "videoChannel" vc
		JOIN account acc ON acc.id = vc."accountId"
		JOIN actor cact ON cact.id = vc."actorId"
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
	rows, err := s.pool.Query(ctx, `
		SELECT v.id, v.uuid::text, v."channelId", v.name, COALESCE(v.description, ''),
		       v.privacy, v.state, v.category, v.licence, v.language, v.duration, v."createdAt"
		FROM video v
		JOIN "videoChannel" vc ON vc.id = v."channelId"
		JOIN actor act ON act.id = vc."actorId"
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
			&v.Privacy, &v.State, &v.Category, &v.Licence, &v.Language, &v.Duration, &v.CreatedAt); err != nil {
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

// ThumbnailFilename returns the miniature thumbnail filename for a video ("" when
// none). PeerTube thumbnail type 1 = MINIATURE.
func (s *Source) ThumbnailFilename(ctx context.Context, videoID int64) (string, error) {
	var filename string
	err := s.pool.QueryRow(ctx, `
		SELECT filename FROM thumbnail
		WHERE "videoId" = $1 AND type = 1
		ORDER BY id LIMIT 1`, videoID).Scan(&filename)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("peertubeimport: read thumbnail: %w", err)
	}
	return filename, nil
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

// Comments returns every local (locally-authored, non-deleted) comment, parents
// before replies (ORDER BY id), so the importer can resolve parent_id from the
// ledger as it goes.
func (s *Source) Comments(ctx context.Context) ([]SourceComment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT vc.id, vc."videoId", acc."userId",
		       COALESCE(vc."inReplyToCommentId", 0), vc.text, vc."createdAt"
		FROM "videoComment" vc
		JOIN account acc ON acc.id = vc."accountId"
		JOIN actor act ON act.id = acc."actorId"
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
	rows, err := s.pool.Query(ctx, `
		SELECT facc."userId", tvc.id
		FROM "actorFollow" af
		JOIN actor fa ON fa.id = af."actorId"
		JOIN account facc ON facc."actorId" = fa.id
		JOIN actor ta ON ta.id = af."targetActorId"
		JOIN "videoChannel" tvc ON tvc."actorId" = ta.id
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
