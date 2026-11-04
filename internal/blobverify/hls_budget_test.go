package blobverify

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestHLSBudgetRefusesBeforeRetainingAndDoesNotChargeDuplicates(t *testing.T) {
	b := hlsBudget{limits: hlsLimits{nodes: 2, keyBytes: 4, depth: 2}}
	for _, key := range []string{"aa", "bb"} {
		if fresh, err := b.admit(key, 1); !fresh || err != nil {
			t.Fatalf("admit %q: %v, %v", key, fresh, err)
		}
	}
	if fresh, err := b.admit("aa", 2); fresh || err != nil || b.usage.nodes != 2 || b.usage.keyBytes != 4 || b.usage.depth != 2 {
		t.Fatalf("duplicate charged or depth lost: %+v, %v, %v", b.usage, fresh, err)
	}
	for _, tc := range []struct {
		name, key string
		limits    hlsLimits
		depth     int
	}{
		{"nodes", "a", hlsLimits{nodes: 0, keyBytes: 10, depth: 2}, 1},
		{"key_bytes", "long", hlsLimits{nodes: 10, keyBytes: 3, depth: 2}, 1},
		{"depth", "a", hlsLimits{nodes: 10, keyBytes: 10, depth: 2}, 3},
	} {
		limited := hlsBudget{limits: tc.limits}
		fresh, err := limited.admit(tc.key, tc.depth)
		if fresh || !errors.Is(err, errHLSLimit) || len(limited.seen) != 0 || limited.usage.nodes != 0 || limited.usage.keyBytes != 0 || limited.usage.exhausted != tc.name {
			t.Errorf("refused admission retained state: %+v, %v, %v", limited, fresh, err)
		}
		if fresh, err := limited.admit("z", 0); fresh || !errors.Is(err, errHLSLimit) || len(limited.seen) != 0 {
			t.Errorf("exhausted tree resumed admission: %+v, %v, %v", limited, fresh, err)
		}
	}
}

type hlsCountingReader struct {
	io.Reader
	read int
}

func (r *hlsCountingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += n
	return n, err
}

func TestHLSBudgetBoundsEveryReadByRemainingBytes(t *testing.T) {
	for _, tc := range []struct {
		name            string
		playlist, total int64
	}{
		{"playlist_bytes", 4, 20}, {"manifest_bytes", 20, 4},
	} {
		b := hlsBudget{limits: hlsLimits{playlistBytes: tc.playlist, manifestBytes: tc.total}}
		r := &hlsCountingReader{Reader: bytes.NewReader(bytes.Repeat([]byte{'x'}, 100))}
		body, err := b.readPlaylist(r)
		if body != nil || !errors.Is(err, errHLSLimit) || r.read != 5 || b.usage.manifestBytes != 4 || b.usage.exhausted != tc.name {
			t.Errorf("read exceeded admitted budget: bytes=%d state=%+v err=%v", r.read, b.usage, err)
		}
		r = &hlsCountingReader{Reader: bytes.NewBufferString("x")}
		if _, err := b.readPlaylist(r); !errors.Is(err, errHLSLimit) || r.read != 0 {
			t.Errorf("exhausted tree resumed reading: %d, %v", r.read, err)
		}
	}
	b := hlsBudget{limits: hlsLimits{playlistBytes: 8, manifestBytes: 10}}
	if body, err := b.readPlaylist(bytes.NewBufferString("12345678")); err != nil || string(body) != "12345678" {
		t.Fatalf("exact playlist ceiling refused: %q, %v", body, err)
	}
	r := &hlsCountingReader{Reader: bytes.NewBufferString("abcd")}
	if _, err := b.readPlaylist(r); !errors.Is(err, errHLSLimit) || r.read != 3 || b.usage.manifestBytes != 10 || b.usage.exhausted != "manifest_bytes" {
		t.Fatalf("remaining total ignored: read=%d state=%+v err=%v", r.read, b.usage, err)
	}
	r = &hlsCountingReader{Reader: bytes.NewBufferString("x")}
	if _, err := b.readPlaylist(r); !errors.Is(err, errHLSLimit) || r.read != 0 {
		t.Fatalf("exhausted total performed another read: %d, %v", r.read, err)
	}
}

type hlsBrokenReader struct{}

func (hlsBrokenReader) Read(p []byte) (int, error) { return copy(p, "xx"), io.ErrUnexpectedEOF }

func TestHLSBudgetPreservesReadFailureWithoutParsingPartialBody(t *testing.T) {
	b := hlsBudget{limits: hlsLimits{playlistBytes: 8, manifestBytes: 10}}
	if body, err := b.readPlaylist(hlsBrokenReader{}); body != nil || !errors.Is(err, io.ErrUnexpectedEOF) || b.usage.manifestBytes != 2 || b.usage.exhausted != "" {
		t.Fatalf("read error lost: body=%q state=%+v err=%v", body, b.usage, err)
	}
}

func TestHLSDefaultLimitsMatchApprovedAdmissionContract(t *testing.T) {
	want := hlsLimits{playlistBytes: 16 << 20, manifestBytes: 128 << 20, nodes: 1000000, keyBytes: 64 << 20, depth: 32}
	if defaultHLSLimits != want {
		t.Fatalf("limits changed: %+v", defaultHLSLimits)
	}
}
