package catalog

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/searchevents"
)

// FullBuilder retains one bounded page plus UUID membership/digests, not all
// request bodies. emit must stage pages in the caller's capture transaction;
// any error poisons the full, whose transaction must then be rolled back.
type FullBuilder struct {
	id      Identity
	at      time.Time
	limits  Limits
	emit    func(FrozenPage) error
	page    *PageBuilder
	seen    map[uuid.UUID]bool
	digests []PageDigest
	bytes   int64
	endID   uuid.UUID
	done    bool
	err     error
}

func NewFullBuilder(id Identity, at time.Time, limits Limits, emit func(FrozenPage) error) (*FullBuilder, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	limits = limits.Effective()
	if limits.MaxRequestBytes <= 0 || limits.MaxSnapshotBytes <= 0 || limits.MaxDocuments <= 0 || limits.MaxPages <= 0 {
		return nil, ErrQuotaExceeded
	}
	if at.IsZero() || emit == nil {
		return nil, fmt.Errorf("catalog full needs immutable time and transactional page staging")
	}
	return &FullBuilder{id: id, at: at, limits: limits, emit: emit, seen: make(map[uuid.UUID]bool), endID: uuid.New()}, nil
}

func (b *FullBuilder) fail(err error) error { b.err = err; return err }

func (b *FullBuilder) Add(doc searchevents.VideoDoc) error {
	if b.err != nil {
		return b.err
	}
	if b.done {
		return fmt.Errorf("catalog full already finalized")
	}
	if b.seen[doc.ID] || doc.ChannelID == uuid.Nil || doc.OwnerID == uuid.Nil {
		return b.fail(fmt.Errorf("catalog full requires distinct canonical ownership and membership"))
	}
	if int64(len(b.seen)) >= b.limits.MaxDocuments {
		return b.fail(ErrQuotaExceeded)
	}
	if b.page == nil {
		page, err := NewPageBuilder(b.id, int32(len(b.digests)), uuid.New(), b.at, b.limits)
		if err != nil {
			return b.fail(err)
		}
		b.page = page
	}
	fits, err := b.page.Add(doc)
	if err != nil {
		return b.fail(err)
	}
	if !fits {
		if b.page.count == 0 {
			return b.fail(ErrQuotaExceeded)
		}
		if err := b.flush(); err != nil {
			return err
		}
		return b.Add(doc)
	}
	b.seen[doc.ID] = true
	return nil
}

func (b *FullBuilder) flush() error {
	if b.page == nil {
		return nil
	}
	page, err := b.page.Freeze()
	if err != nil {
		return b.fail(err)
	}
	if b.bytes+page.CanonicalBytes > b.limits.MaxSnapshotBytes {
		return b.fail(ErrQuotaExceeded)
	}
	if err := b.emit(page); err != nil {
		return b.fail(err)
	}
	b.bytes += page.CanonicalBytes
	b.digests = append(b.digests, PageDigest{page.PageIndex, page.ContentSHA256})
	b.page = nil
	return nil
}

func (b *FullBuilder) Finish() (FrozenEnd, error) {
	if b.err != nil {
		return FrozenEnd{}, b.err
	}
	if b.done {
		return FrozenEnd{}, fmt.Errorf("catalog full already finalized")
	}
	if err := b.flush(); err != nil {
		return FrozenEnd{}, err
	}
	ids := make([]uuid.UUID, 0, len(b.seen))
	for id := range b.seen {
		ids = append(ids, id)
	}
	membership, err := MembershipDigest(ids)
	if err != nil {
		return FrozenEnd{}, b.fail(err)
	}
	content, err := PagesDigest(b.digests)
	if err != nil {
		return FrozenEnd{}, b.fail(err)
	}
	end, err := FreezeEnd(b.id, Manifest{int32(len(b.digests)), int64(len(ids)), membership, content}, b.bytes, b.endID, b.at, b.limits)
	if err != nil {
		return FrozenEnd{}, b.fail(err)
	}
	b.done = true
	return end, nil
}
