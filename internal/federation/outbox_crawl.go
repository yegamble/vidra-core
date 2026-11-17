package federation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/urlsafety"
)

// Reading a followed actor's outbox.
//
// A remote server pushes only what it publishes AFTER a follow is accepted.
// What it published before reaches a follower only if the follower reads the
// followed actor's outbox. PeerTube does, straight after the Accept
// (process-accept.ts); vidra did not, so a channel or an instance it followed
// showed nothing until its next upload. The interop lab staged a public video
// on PeerTube before each kind of follow and neither arrived (meta
// docs/evidence/peertube-interop-2026-11-17-gaps.md).
//
// CrawlOutbox is that read. It has NO ingest rules of its own: each activity
// it finds goes to the handler an inbox delivery of it reaches
// (handleAnnounce, handleCreateVideo), with the crawled actor standing where
// the signer stands. An activity listed in an actor's outbox, fetched from
// that actor's own host, is that host speaking for that actor — the authority
// a signed delivery carries. So the same rules hold, unchanged: the object
// lives on the actor's host, somebody here follows the actor or its instance,
// and the video is filed under its channel (videoOwner). What it adds is what
// a delivery never needed: bounds on how much it reads.

const (
	// outboxCrawlMaxPages bounds how many pages one crawl reads after the
	// outbox document itself. PeerTube's pages hold 10 activities and vidra's
	// 20, newest first, so this is the newest hundred or two — the rest is
	// history a follower did not ask to mirror.
	outboxCrawlMaxPages = 10
	// maxOutboxPageBytes bounds one outbox document. A page of 20 inline
	// Video objects is a few hundred KiB.
	maxOutboxPageBytes = 2 << 20
)

// outboxPage is what the crawl reads of an OrderedCollection or one of its
// pages: the activities, and the link to follow next (`first` on the outbox
// itself, `next` on a page).
type outboxPage struct {
	First        json.RawMessage   `json:"first"`
	Next         json.RawMessage   `json:"next"`
	OrderedItems []json.RawMessage `json:"orderedItems"`
}

// CrawlOutbox reads the outbox of a remote actor this instance follows and
// ingests the videos it lists that are not held yet. It returns how many it
// stored.
//
// It is idempotent: a held video is neither fetched nor written again. An
// actor nobody follows, or one on a blocked instance, is not read at all.
// Every request goes through the SSRF guard and stays on the actor's host. A
// page that cannot be read ends the crawl with its error; an activity that
// cannot be ingested is skipped, and the first such error is returned with
// the count once every page has been read, so a caller may retry.
func (s *Service) CrawlOutbox(ctx context.Context, actorURL string) (int, error) {
	if blocked, err := s.signerDomainBlocked(ctx, actorURL); err != nil || blocked {
		return 0, err
	}
	if ok, err := s.hasAcceptedFollowEdge(ctx, actorURL); err != nil || !ok {
		return 0, err
	}
	// The outbox URL is read from the actor's own document, not derived. The
	// document is cached as well: a delivery's handler may take its signer for
	// cached (videoOwner), and here the crawled actor is in the signer's place.
	actor, err := s.fetchActor(ctx, actorURL)
	if err != nil {
		return 0, err
	}
	if _, err := s.cacheFetchedActor(ctx, actorURL, actor); err != nil {
		return 0, err
	}
	stored, seen := 0, map[string]bool{}
	var skipped error
	next := actor.Outbox
	for read := 0; read <= outboxCrawlMaxPages && sameHost(next, actorURL) && !seen[next]; read++ {
		seen[next] = true
		page, err := s.fetchOutboxPage(ctx, next)
		if err != nil {
			return stored, err
		}
		for _, item := range page.OrderedItems {
			ok, err := s.ingestOutboxItem(ctx, actorURL, item)
			if ok {
				stored++
			}
			if err != nil && skipped == nil {
				skipped = err
			}
		}
		if next = objectID(page.Next); next == "" {
			next = objectID(page.First)
		}
	}
	return stored, skipped
}

// ingestOutboxItem hands one outbox activity to the handler a delivery of it
// reaches, and reports whether a video was stored. Only what the crawled actor
// itself did is taken, only a video, and only one on the actor's host that is
// not held yet.
func (s *Service) ingestOutboxItem(ctx context.Context, actorURL string, raw json.RawMessage) (bool, error) {
	var act inboxActivity
	if json.Unmarshal(raw, &act) != nil || act.Actor != actorURL {
		return false, nil
	}
	if act.Type != "Announce" && (act.Type != "Create" || objectType(act.Object) != "Video") {
		return false, nil
	}
	objectURL := objectID(act.Object)
	if !sameHost(objectURL, actorURL) {
		return false, nil
	}
	// The crawl backfills. An edit of a held video arrives as an Update.
	if held, err := s.remoteVideoHeld(ctx, objectURL); err != nil || held {
		return false, err
	}
	var err error
	if act.Type == "Announce" {
		err = s.handleAnnounce(ctx, act, actorURL)
	} else {
		err = s.handleCreateVideo(ctx, act, actorURL)
	}
	if err != nil {
		return false, err
	}
	return s.remoteVideoHeld(ctx, objectURL)
}

func (s *Service) remoteVideoHeld(ctx context.Context, objectURL string) (bool, error) {
	_, err := s.repo.GetRemoteVideoByObjectURL(ctx, objectURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// fetchOutboxPage GETs an outbox document through the SSRF guard, bounded to
// maxOutboxPageBytes. A redirect may not leave the host.
func (s *Service) fetchOutboxPage(ctx context.Context, pageURL string) (outboxPage, error) {
	guard := urlsafety.Guard{AllowPrivate: s.allowPrivateFetch}
	target, err := guard.ValidateURL(pageURL)
	if err != nil {
		return outboxPage{}, fmt.Errorf("federation: unsafe outbox URL: %w", err)
	}
	req, err := http.NewRequestWithContext(withSameHostRedirects(ctx), http.MethodGet, target.String(), nil)
	if err != nil {
		return outboxPage{}, err
	}
	req.Header.Set("Accept", "application/activity+json")
	resp, err := s.remoteClient(guard).Do(req)
	if err != nil {
		return outboxPage{}, fmt.Errorf("federation: fetch outbox: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return outboxPage{}, fmt.Errorf("federation: outbox fetch returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOutboxPageBytes))
	if err != nil {
		return outboxPage{}, err
	}
	var page outboxPage
	if err := json.Unmarshal(body, &page); err != nil {
		return outboxPage{}, fmt.Errorf("federation: parse outbox: %w", err)
	}
	return page, nil
}
