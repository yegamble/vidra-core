package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeVideoPassword is one stored password in the in-memory video repo fake.
type fakeVideoPassword struct {
	ID        uuid.UUID
	Hash      string
	CreatedAt time.Time
}

func (f *videoFakeRepo) ListVideoPasswords(_ context.Context, videoID uuid.UUID) ([]sqlcgen.ListVideoPasswordsRow, error) {
	out := make([]sqlcgen.ListVideoPasswordsRow, 0, len(f.passwords[videoID]))
	for _, p := range f.passwords[videoID] {
		out = append(out, sqlcgen.ListVideoPasswordsRow{ID: p.ID, CreatedAt: p.CreatedAt})
	}
	return out, nil
}

func (f *videoFakeRepo) ListVideoPasswordHashes(_ context.Context, videoID uuid.UUID) ([]string, error) {
	out := make([]string, 0, len(f.passwords[videoID]))
	for _, p := range f.passwords[videoID] {
		out = append(out, p.Hash)
	}
	return out, nil
}

func (f *videoFakeRepo) CountVideoPasswords(_ context.Context, videoID uuid.UUID) (int64, error) {
	return int64(len(f.passwords[videoID])), nil
}

func (f *videoFakeRepo) InsertVideoPassword(_ context.Context, a sqlcgen.InsertVideoPasswordParams) (sqlcgen.InsertVideoPasswordRow, error) {
	if f.passwords == nil {
		f.passwords = map[uuid.UUID][]fakeVideoPassword{}
	}
	p := fakeVideoPassword{ID: uuid.New(), Hash: a.PasswordHash, CreatedAt: time.Now()}
	f.passwords[a.VideoID] = append(f.passwords[a.VideoID], p)
	return sqlcgen.InsertVideoPasswordRow{ID: p.ID, CreatedAt: p.CreatedAt}, nil
}

func (f *videoFakeRepo) VideoPasswordExists(_ context.Context, a sqlcgen.VideoPasswordExistsParams) (bool, error) {
	for _, p := range f.passwords[a.VideoID] {
		if p.ID == a.ID {
			return true, nil
		}
	}
	return false, nil
}

func (f *videoFakeRepo) DeleteVideoPassword(_ context.Context, a sqlcgen.DeleteVideoPasswordParams) (int64, error) {
	rows := f.passwords[a.VideoID]
	for i, p := range rows {
		if p.ID == a.ID {
			f.passwords[a.VideoID] = append(rows[:i], rows[i+1:]...)
			return 1, nil
		}
	}
	return 0, nil
}

func (f *videoFakeRepo) DeleteVideoPasswords(_ context.Context, videoID uuid.UUID) error {
	delete(f.passwords, videoID)
	return nil
}

func (f *videoFakeRepo) InsertVideoPasswords(_ context.Context, a sqlcgen.InsertVideoPasswordsParams) error {
	if f.passwords == nil {
		f.passwords = map[uuid.UUID][]fakeVideoPassword{}
	}
	for _, h := range a.PasswordHash {
		f.passwords[a.VideoID] = append(f.passwords[a.VideoID], fakeVideoPassword{ID: uuid.New(), Hash: h, CreatedAt: time.Now()})
	}
	return nil
}

func (f *videoFakeRepo) GetVideoEmbedPrivacy(_ context.Context, videoID uuid.UUID) (sqlcgen.GetVideoEmbedPrivacyRow, error) {
	if _, ok := f.videos[videoID]; !ok {
		return sqlcgen.GetVideoEmbedPrivacyRow{}, errors.New("not found")
	}
	if row, ok := f.embed[videoID]; ok {
		return row, nil
	}
	// Column default: every video is embeddable with no domain restriction.
	return sqlcgen.GetVideoEmbedPrivacyRow{EmbedPrivacy: "enabled", EmbedAllowedDomains: []string{}}, nil
}

func (f *videoFakeRepo) SetVideoEmbedPrivacy(_ context.Context, a sqlcgen.SetVideoEmbedPrivacyParams) error {
	if f.embed == nil {
		f.embed = map[uuid.UUID]sqlcgen.GetVideoEmbedPrivacyRow{}
	}
	domains := a.EmbedAllowedDomains
	if domains == nil {
		domains = []string{}
	}
	f.embed[a.ID] = sqlcgen.GetVideoEmbedPrivacyRow{EmbedPrivacy: a.EmbedPrivacy, EmbedAllowedDomains: domains}
	return nil
}
