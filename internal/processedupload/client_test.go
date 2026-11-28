package processedupload

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
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
		{"clean", true, false, nil}, {"infected", false, false, nil}, {"unavailable", false, false, errors.New("scan failed")}, {"incomplete", true, true, nil},
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
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			b, err := io.ReadAll(f)
			if err != nil || string(b) != processed {
				t.Fatal("returned different bytes from the scan")
			}
		})
	}
}
