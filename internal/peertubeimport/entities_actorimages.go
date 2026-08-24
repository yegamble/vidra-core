package peertubeimport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/profileimage"
	"github.com/vidra/vidra-core/internal/urlsafety"
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
// ── why this family is fetched over HTTP and not copied out of storage ──
//
// Every other media family the importer moves is read through srcMedia, the
// source's object store. Actor images are not there. PeerTube's object-storage
// config covers streaming playlists, web videos, captions and originals — NOT
// avatars, which stay on the source host's local filesystem no matter how the
// rest of the instance is configured. So --source-storage=s3 cannot see them,
// and --source-local-root cannot either unless the import happens to run on the
// source host itself.
//
// They ARE served publicly, by the source instance, at
// <origin>/lazy-static/avatars/<filename>. That endpoint is the only way to get
// these bytes from another machine, so that is what this pass uses.
//
// NOTE the path: /static/avatars/<filename> answers 200 with the single-page
// app's HTML shell, not a 404. An implementation that trusted the status code
// would store 62 KB of HTML as every account's avatar. Hence the content-type
// gate below, which checks what the bytes ARE and not what the response claims.

// Actor-image tuning. The source is a LIVE PRODUCTION instance during a
// migration — the operator re-runs this on a schedule right up to cutover — so
// the client is deliberately unhurried: a handful of connections, a bounded
// wait, and a size cap well under the source's own upload limit.
const (
	actorImageConcurrency = 4
	actorImageTimeout     = 20 * time.Second
	maxActorImageBytes    = 8 << 20 // 8 MiB, matching HTTP_BODY_LIMIT's default
	actorImageLazyStatic  = "/lazy-static/avatars/"
	actorImageUserAgent   = "vidra-peertube-import/1.0"
)

// errActorImageNotAnImage is the content-type gate's rejection: the source
// answered, but with something that is not an image Vidra stores. It is
// recorded as unsupported (a fact about the source) rather than failed (a
// transient problem worth retrying).
var errActorImageNotAnImage = errors.New("peertubeimport: response is not a supported image")

// actorImageFetcher pulls avatar/banner bytes from the source instance's public
// origin. The origin is PINNED for the whole run: one host is contacted, and a
// filename from the source database can only ever select a leaf under one known
// prefix (path.Base strips any directory the source recorded), so no row in the
// source database can steer a fetch somewhere else.
type actorImageFetcher struct {
	origin string
	client *http.Client
}

func newActorImageFetcher(origin string) *actorImageFetcher {
	// AllowPrivate is set on purpose. A migration source is routinely reachable
	// only on a LAN or through a tunnel, and this URL is not user input: it is
	// derived from the operator's own source database, the same database this
	// tool already reads password hashes and actor private keys out of. The
	// scheme/userinfo checks and the per-redirect re-validation still apply.
	client := urlsafety.Guard{AllowPrivate: true}.NewClient(actorImageTimeout)
	return &actorImageFetcher{origin: strings.TrimSuffix(origin, "/"), client: client}
}

// fetch returns the image bytes and the extension they should be stored under.
// The extension comes from what the bytes ARE, never from the source filename:
// the stored content type is derived from the extension downstream, so trusting
// a filename would let a mislabelled source file be served as the wrong type.
func (f *actorImageFetcher) fetch(ctx context.Context, filename string) ([]byte, string, error) {
	name := path.Base(strings.TrimSpace(filename))
	if name == "" || name == "." || name == "/" {
		return nil, "", fmt.Errorf("%w: empty filename", errActorImageNotAnImage)
	}
	target := f.origin + actorImageLazyStatic + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", actorImageUserAgent)
	req.Header.Set("Accept", "image/*")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("peertubeimport: source answered %d for actor image", resp.StatusCode)
	}
	// First gate, before a single byte of body is read: the SPA fallback answers
	// text/html, and there is no reason to download a page to find that out. A
	// response that declares NOTHING falls through to the sniff instead of being
	// rejected — the header is here to save a download, not to be the ruling.
	declared, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	declared = strings.ToLower(strings.TrimSpace(declared))
	if declared != "" && !strings.HasPrefix(declared, "image/") {
		return nil, "", fmt.Errorf("%w: declared %q", errActorImageNotAnImage, declared)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxActorImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > maxActorImageBytes {
		return nil, "", fmt.Errorf("peertubeimport: actor image exceeds the %d-byte cap", int64(maxActorImageBytes))
	}
	if len(body) == 0 {
		return nil, "", fmt.Errorf("%w: empty body", errActorImageNotAnImage)
	}
	// Second gate, and the authoritative one: sniff the bytes. A source that
	// declares image/png and serves anything else is caught here, and the
	// extension the object is stored under comes from this answer.
	ext := imageExtForSniffedType(sniffContentType(body))
	if ext == "" {
		return nil, "", fmt.Errorf("%w: sniffed %q", errActorImageNotAnImage, sniffContentType(body))
	}
	return body, ext, nil
}

// sniffContentType classifies the leading bytes of a response body. It is
// http.DetectContentType with the standard 512-byte window.
func sniffContentType(body []byte) string {
	head := body
	if len(head) > 512 {
		head = head[:512]
	}
	return strings.ToLower(strings.TrimSpace(strings.SplitN(http.DetectContentType(head), ";", 2)[0]))
}

// ── the pass ──

// actorImageTarget is one source image resolved against this Vidra instance:
// which slot it fills and whose it is.
type actorImageTarget struct {
	img       SourceActorImage
	sourceID  string
	ledgerKnd string
	imageKind string // profileimage.KindAvatar / KindBanner
	userID    uuid.UUID
	channelID uuid.UUID
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

	fetcher := newActorImageFetcher(origin)
	im.logger.InfoContext(ctx, "peertube import: fetching actor images",
		"origin", origin, "images", len(targets), "concurrency", actorImageConcurrency)

	// Bounded concurrency: a few connections to one live production host, not a
	// thundering herd. A per-image failure is recorded and the run continues —
	// nobody's migration should stop because one avatar 404s.
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		work = make(chan actorImageTarget)
	)
	for i := 0; i < actorImageConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range work {
				err := im.importOneActorImage(ctx, fetcher, svc, t, &mu, r)
				if err == nil {
					continue
				}
				im.markFailed(ctx, t.ledgerKnd, t.sourceID, safeErr(err))
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
// actually do, recording a terminal ledger row for everything it decides NOT to
// fetch. Every decision that can be made from the database is made here, before
// a single HTTP request leaves the machine — the source is live, and the most
// considerate request is the one that is never sent.
func (im *Importer) resolveActorImageTargets(
	ctx context.Context,
	svc *profileimage.Service,
	images []SourceActorImage,
	r *Report,
) ([]actorImageTarget, error) {
	var targets []actorImageTarget
	for _, img := range images {
		imageKind, ok := mapActorImageKind(img.Type)
		if !ok {
			// Unknown ActorImageType: it belongs in a slot this tool cannot name.
			// Recorded under the avatar kind purely so the row is visible.
			sid := strconv.FormatInt(img.ID, 10)
			if _, _, done, err := im.alreadyProcessed(ctx, KindActorAvatar, sid); err != nil {
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
		if _, _, done, err := im.alreadyProcessed(ctx, ledgerKnd, sid); err != nil {
			return nil, err
		} else if done {
			// The whole idempotency story: a scheduled re-run re-fetches nothing.
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
				// No terminal row: the owner may be imported by a later run (the
				// same rule the per-video families follow), and then the image
				// should follow it in.
				r.count(ledgerKnd).Skipped++
				continue
			}
			if svc.HasUserImage(ctx, id, imageKind) {
				if err := im.recordStandalone(ctx, ledgerKnd, sid, id, "skipped", "account already has a "+imageKind+" on this instance"); err != nil {
					return nil, err
				}
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
			if svc.HasChannelImage(ctx, id, imageKind) {
				if err := im.recordStandalone(ctx, ledgerKnd, sid, id, "skipped", "channel already has a "+imageKind+" on this instance"); err != nil {
					return nil, err
				}
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
		targets = append(targets, t)
	}
	return targets, nil
}

// importOneActorImage fetches one image and writes it through the normal
// profile-image path, so it lands on whatever backend the instance stores media
// on, under the same key layout an upload would use.
func (im *Importer) importOneActorImage(
	ctx context.Context,
	fetcher *actorImageFetcher,
	svc *profileimage.Service,
	t actorImageTarget,
	mu *sync.Mutex,
	r *Report,
) error {
	body, ext, err := fetcher.fetch(ctx, t.img.Filename)
	if err != nil {
		if errors.Is(err, errActorImageNotAnImage) {
			// A fact about the source, not a transient failure: recording it
			// terminal stops the next run asking the same question again.
			if err := im.recordStandalone(ctx, t.ledgerKnd, t.sourceID, uuid.Nil, "unsupported", "source did not serve a JPEG, PNG or WebP"); err != nil {
				return err
			}
			mu.Lock()
			r.count(t.ledgerKnd).Unsupported++
			mu.Unlock()
			return nil
		}
		return err
	}

	// The filename here exists only to carry the extension: profileimage derives
	// both the stored content type and the object key's suffix from it, and the
	// extension came from sniffing the bytes above.
	in := profileimage.UploadInput{Filename: "import" + ext, Reader: bytes.NewReader(body)}
	var owner uuid.UUID
	if t.userID != uuid.Nil {
		if _, err := svc.SetUserImage(ctx, t.userID, t.imageKind, in); err != nil {
			return err
		}
		owner = t.userID
	} else {
		if _, err := svc.SetChannelImage(ctx, t.channelID, t.imageKind, in); err != nil {
			return err
		}
		owner = t.channelID
	}
	// The ledger row lands after the write rather than inside it: the blob and
	// the image row are not one transaction (no blob write ever is). A crash in
	// the gap leaves the image correctly in place with no ledger row, and the
	// next run sees the filled slot and records it skipped — the picture is
	// right either way, only the note is less precise.
	if err := im.recordStandalone(ctx, t.ledgerKnd, t.sourceID, owner, "done", ""); err != nil {
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

// planActorImages adds the two actor-image families to a dry-run plan: the rows
// the import would consider, which is every actorImage belonging to a LOCAL
// actor. A source with no actorImage table contributes a deferred note instead.
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
