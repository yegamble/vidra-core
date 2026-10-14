package peertubearchive

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/profileimage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jpegBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewGray(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// hugePNG is a valid PNG header that DECLARES a 60000x60000 image: DecodeConfig
// reads only the header, so this is a few dozen bytes standing for ~3.6 GP.
func hugePNG() []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 60000)
	binary.BigEndian.PutUint32(ihdr[4:], 60000)
	ihdr[8], ihdr[9] = 8, 0
	chunk := append([]byte("IHDR"), ihdr...)
	_ = binary.Write(&b, binary.BigEndian, uint32(13))
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

type fakeImages struct {
	have map[string]bool
	set  map[string][]byte
	err  error
}

func (f *fakeImages) HasChannelImage(_ context.Context, id uuid.UUID, kind string) bool {
	return f.have[id.String()+kind]
}
func (f *fakeImages) SetChannelImage(_ context.Context, id uuid.UUID, kind string, in profileimage.UploadInput) (profileimage.Image, error) {
	if f.err != nil {
		return profileimage.Image{}, f.err
	}
	b := new(bytes.Buffer)
	_, _ = b.ReadFrom(in.Reader)
	if f.set == nil {
		f.set = map[string][]byte{}
	}
	f.set[kind+":"+in.Filename] = b.Bytes()
	f.have[id.String()+kind] = true
	return profileimage.Image{}, nil
}

func imageImport(t *testing.T, imgs *fakeImages, scan func(context.Context, string, []byte) error, ch Channel, entries ...entry) Summary {
	t.Helper()
	entries = append(entries, entry{name: "peertube/channels.json", body: `{"channels":[{"name":"` + ch.Name + `"}]}`})
	a, err := Read(buildZip(t, entries...))
	if err != nil {
		t.Fatal(err)
	}
	a.Channels = []Channel{ch}
	im := &Importer{Channels: &fakeChannels{byHandle: map[string]sqlcgen.Channel{}}, Playlists: &fakePlaylists{}, Images: imgs, ScanImage: scan}
	sum, err := im.Import(context.Background(), uuid.New(), a)
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

func chanWith(avatar, banner string) Channel {
	c := Channel{Name: "my_chan"}
	c.ArchiveFiles.Avatar, c.ArchiveFiles.Banner = avatar, banner
	return c
}

func TestImportChannelImages(t *testing.T) {
	imgs := &fakeImages{have: map[string]bool{}}
	sum := imageImport(t, imgs, nil, chanWith("video-channels/1/avatar.png", "files/banner.jpg"),
		entry{name: "files/video-channels/1/avatar.png", body: string(pngBytes(t, 8, 8))},
		entry{name: "files/banner.jpg", body: string(jpegBytes(t))})
	if sum.ChannelImagesSet != 2 || sum.ChannelImagesSkipped != 0 || len(imgs.set) != 2 {
		t.Fatalf("%+v %v", sum, imgs.set)
	}
}

func TestImportChannelImagesAbsentIsSilentAndExistingKept(t *testing.T) {
	imgs := &fakeImages{have: map[string]bool{}}
	if sum := imageImport(t, imgs, nil, chanWith("", "")); sum.ChannelImagesSet+sum.ChannelImagesSkipped != 0 {
		t.Fatalf("absent must be silent: %+v", sum)
	}
	// A channel that already has an avatar keeps it: nothing is read or set.
	imgs = &fakeImages{have: map[string]bool{}}
	a, _ := Read(buildZip(t, entry{name: "peertube/channels.json", body: `{"channels":[{"name":"my_chan"}]}`},
		entry{name: "files/a.png", body: string(pngBytes(t, 4, 4))}))
	a.Channels = []Channel{chanWith("a.png", "")}
	ch := &fakeChannels{byHandle: map[string]sqlcgen.Channel{}}
	im := &Importer{Channels: ch, Playlists: &fakePlaylists{}, Images: imgs}
	if _, err := im.Import(context.Background(), uuid.New(), a); err != nil || len(imgs.set) != 1 {
		t.Fatalf("first: %v %v", err, imgs.set)
	}
	imgs.set = nil
	sum, err := im.Import(context.Background(), ch.byHandle["my_chan"].OwnerID, a)
	if err != nil || len(imgs.set) != 0 || sum.ChannelImagesSet != 0 {
		t.Fatalf("re-import replaced an image: %+v %v", sum, imgs.set)
	}
}

func TestImportChannelImagesRefusesHostileEntries(t *testing.T) {
	big := string(pngBytes(t, 8, 8)) + strings.Repeat("A", maxImageBytes) // over the byte cap
	cases := map[string][]entry{
		"not an image behind a .png name": {{name: "files/a.png", body: "<html><script>alert(1)</script></html>"}},
		"jpeg bytes under a .png name":    {{name: "files/a.png", body: string(jpegBytes(t))}},
		"unsupported extension":           {{name: "files/a.svg", body: "<svg/>"}},
		"gif extension":                   {{name: "files/a.gif", body: "GIF89a"}},
		"decompression-bomb dimensions":   {{name: "files/a.png", body: string(hugePNG())}},
		"over the byte cap":               {{name: "files/a.png", body: big}},
		"truncated image":                 {{name: "files/a.png", body: string(pngBytes(t, 8, 8))[:20]}},
		"reference outside files/":        {{name: "peertube/a.png", body: string(pngBytes(t, 4, 4))}},
		"missing entry":                   nil,
	}
	refs := map[string]string{"reference outside files/": "peertube/a.png", "unsupported extension": "a.svg", "gif extension": "a.gif"}
	for name, ents := range cases {
		ref := refs[name]
		if ref == "" {
			ref = "a.png"
		}
		imgs := &fakeImages{have: map[string]bool{}}
		sum := imageImport(t, imgs, nil, chanWith(ref, ""), ents...)
		if sum.ChannelImagesSet != 0 || sum.ChannelImagesSkipped != 1 || len(imgs.set) != 0 {
			t.Errorf("%s: stored or uncounted: %+v", name, sum)
		}
	}
}

func TestImportChannelImagesTraversalReferencesResolveToNothing(t *testing.T) {
	a, _ := Read(buildZip(t, entry{name: "peertube/channels.json", body: `{"channels":[{"name":"my_chan"}]}`},
		entry{name: "files/a.png", body: string(pngBytes(t, 4, 4))}))
	for _, ref := range []string{"../files/a.png", "files/../files/a.png", "/files/a.png", `files\a.png`, "files/", strings.Repeat("a/", 400) + "a.png"} {
		if got := a.ImageEntry(ref); got != "" {
			t.Errorf("%q resolved to %q", ref, got)
		}
	}
	if a.ImageEntry("a.png") != "files/a.png" || a.ImageEntry("files/a.png") != "files/a.png" {
		t.Fatal("legitimate reference not resolved")
	}
}

func TestImportChannelImagesScanAndTotalBudget(t *testing.T) {
	imgs := &fakeImages{have: map[string]bool{}}
	reject := func(context.Context, string, []byte) error { return errors.New("infected") }
	sum := imageImport(t, imgs, reject, chanWith("a.png", ""), entry{name: "files/a.png", body: string(pngBytes(t, 4, 4))})
	if sum.ChannelImagesSet != 0 || sum.ChannelImagesSkipped != 1 || len(imgs.set) != 0 {
		t.Fatalf("scan refusal must skip: %+v", sum)
	}
	// Storage trouble skips the image; a database error aborts.
	imgs = &fakeImages{have: map[string]bool{}, err: profileimage.ErrStorageUnavailable}
	sum = imageImport(t, imgs, nil, chanWith("a.png", ""), entry{name: "files/a.png", body: string(pngBytes(t, 4, 4))})
	if sum.ChannelImagesSkipped != 1 {
		t.Fatalf("%+v", sum)
	}
}

func TestReadImageTotalBudgetIsEnforced(t *testing.T) {
	a, _ := Read(buildZip(t, entry{name: "peertube/channels.json", body: `{"channels":[]}`},
		entry{name: "files/a.png", body: string(pngBytes(t, 4, 4))}))
	budget := int64(10)
	if _, _, err := a.ReadImage("files/a.png", &budget); err == nil {
		t.Fatal("read past the total budget")
	}
}
