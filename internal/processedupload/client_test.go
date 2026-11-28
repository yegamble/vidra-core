package processedupload

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
)

type scanFunc func(context.Context, io.Reader) (bool, error)

func (s scanFunc) ScanReader(ctx context.Context, r io.Reader) (bool, error) { return s(ctx, r) }

func TestOnlyTheCompleteCleanDerivativeCanBeAttached(t *testing.T) {
	const processed = "reconstructed-media"
	t.Chdir(t.TempDir())
	socket := "broker"
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil || string(raw) != "private-input" || r.URL.Path != "/normalize" {
			t.Errorf("unexpected processor input")
			w.WriteHeader(422)
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		io.WriteString(w, processed)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	for _, test := range []struct {
		name         string
		clean, early bool
		scanErr      error
	}{
		{"clean", true, false, nil},
		{"infected", false, false, nil},
		{"unavailable", false, false, errors.New("scan failed")},
		{"incomplete", true, true, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			scan := scanFunc(func(_ context.Context, r io.Reader) (bool, error) {
				if !test.early {
					b, err := io.ReadAll(r)
					if err != nil || string(b) != processed {
						t.Fatalf("unexpected scan input")
					}
				}
				return test.clean, test.scanErr
			})
			client := Client{Socket: socket, Scanner: scan}
			f, err := client.Normalize(context.Background(), strings.NewReader("private-input"), 13)
			if test.name != "clean" {
				if f != nil || err == nil {
					t.Fatal("returned an unverified download")
				}
				if test.scanErr != nil && !errors.Is(err, test.scanErr) {
					t.Fatalf("lost scanner error: %v", err)
				}
				if errors.Is(err, ErrInfected) != (test.name == "infected") {
					t.Fatalf("unexpected malware classification: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := os.Stat(f.Name()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("scan scratch file has a directory entry: %v", err)
			}
			b, err := io.ReadAll(f)
			if err != nil || string(b) != processed {
				t.Fatal("returned different bytes from the scan")
			}
		})
	}
}

func TestScanFileLengthMatchesTheScannedContent(t *testing.T) {
	const body = "complete candidate"
	inspect := scanFunc(func(_ context.Context, r io.Reader) (bool, error) {
		_, err := io.Copy(io.Discard, r)
		return err == nil, err
	})
	for _, size := range []int64{0, int64(len(body) - 1), int64(len(body) + 1), MaxScannedBytes + 1} {
		file, err := ScanFile(t.Context(), inspect, strings.NewReader(body), size)
		if file != nil || err == nil || errors.Is(err, ErrInfected) {
			t.Fatalf("size %d returned file %v, err %v", size, file, err)
		}
	}
}
