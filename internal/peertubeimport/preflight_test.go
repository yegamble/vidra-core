package peertubeimport

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

// probeBackend is a destination store that can refuse either half of the write
// probe, and remembers what it was asked to do. It embeds a real Local so
// everything it does not break behaves normally.
type probeBackend struct {
	storage.Backend
	putErr    error
	deleteErr error

	puts    []string
	deletes []string
}

func newProbeBackend(t *testing.T) *probeBackend {
	t.Helper()
	local, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	return &probeBackend{Backend: local}
}

func (b *probeBackend) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	b.puts = append(b.puts, key)
	if b.putErr != nil {
		return 0, b.putErr
	}
	return b.Backend.Put(ctx, key, r)
}

func (b *probeBackend) Delete(ctx context.Context, key string) error {
	b.deletes = append(b.deletes, key)
	if b.deleteErr != nil {
		return b.deleteErr
	}
	return b.Backend.Delete(ctx, key)
}

// The failure this exists for, in one test: the destination credential could
// read but not write, and the run found out 1,321 avatar uploads in.
func TestDestinationWritableRefusesAReadOnlyCredential(t *testing.T) {
	dest := newProbeBackend(t)
	dest.putErr = errors.New(`s3: put "avatars/users/x.jpg": not entitled`)
	im := &Importer{destMedia: dest, mediaMode: MediaModeCopy}

	err := im.checkDestinationWritable(context.Background())
	if err == nil {
		t.Fatal("preflight accepted a destination that refuses writes")
	}
	// The message is persisted on the run row and shown to an admin, so it has to
	// name the capability rather than quote a stack of wrapped errors.
	for _, want := range []string{"writeFiles", "s3:PutObject"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name what to grant (%q): %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "not entitled") == false {
		t.Errorf("the error drops the store's own refusal: %v", err)
	}
}

// The probe must leave the destination exactly as it found it — an import that
// littered the bucket it is about to fill would be its own problem.
func TestDestinationWritableLeavesNothingBehind(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dest, err := storage.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	im := &Importer{destMedia: dest, mediaMode: MediaModeCopy}
	if err := im.checkDestinationWritable(ctx); err != nil {
		t.Fatalf("checkDestinationWritable on a writable store: %v", err)
	}
	keys, err := dest.ListAllKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Errorf("the probe left %v in the destination", keys)
	}
}

// A destination that takes the write and refuses the delete has answered the
// question the import asked. Failing the run on it would refuse to migrate into
// a store that can hold the migration.
func TestDestinationWritableToleratesAFailedCleanup(t *testing.T) {
	dest := newProbeBackend(t)
	dest.deleteErr = errors.New("not entitled")
	im := &Importer{destMedia: dest, mediaMode: MediaModeCopy}

	if err := im.checkDestinationWritable(context.Background()); err != nil {
		t.Fatalf("a failed cleanup aborted a run whose destination is writable: %v", err)
	}
}

// Which modes write, and which do not. Reference mode is the subtle one: it
// records the source's own object keys for video, and still FETCHES actor images
// over HTTP and stores them, so it writes.
func TestDestinationWritableProbesEveryModeThatWrites(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      MediaMode
		dest      bool
		wantProbe bool
	}{
		{name: "copy writes every media family", mode: MediaModeCopy, dest: true, wantProbe: true},
		{name: "reference still stores actor images", mode: MediaModeReference, dest: true, wantProbe: true},
		{name: "none writes no media at all", mode: MediaModeNone, dest: true, wantProbe: false},
		{name: "a metadata-only run has no destination store", mode: MediaModeCopy, dest: false, wantProbe: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			im := &Importer{mediaMode: tc.mode}
			var dest *probeBackend
			if tc.dest {
				dest = newProbeBackend(t)
				im.destMedia = dest
			}
			if err := im.checkDestinationWritable(context.Background()); err != nil {
				t.Fatalf("checkDestinationWritable: %v", err)
			}
			if tc.dest && (len(dest.puts) > 0) != tc.wantProbe {
				t.Errorf("puts = %v, want probe = %v", dest.puts, tc.wantProbe)
			}
		})
	}
}

// The two shapes tonight's failures actually took, plus the ones next to them.
// The advice is the whole point: `dial unix /tmp/.s.PGSQL.15432` does not look
// like "your --source-dsn has no host in it" to anybody reading it at 1am.
func TestSourceDialAdviceNamesTheLikelyCause(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{
			name: "a host that silently became a unix socket",
			err:  errors.New("failed to connect to `host=/tmp user=vidra database=peertube`: dial error (dial unix /tmp/.s.PGSQL.15432: connect: no such file or directory)"),
			want: "unix socket",
		},
		{
			name: "a source Postgres bound to 127.0.0.1 only",
			err:  errors.New("failed to connect: dial tcp 203.0.113.9:5432: connect: connection refused"),
			want: "listening",
		},
		{
			name: "a firewall dropping the packets",
			err:  errors.New("failed to connect: dial tcp 203.0.113.9:5432: i/o timeout"),
			want: "dropped",
		},
		{
			name: "a name that does not resolve",
			err:  errors.New("failed to connect: lookup peertube.invalid: no such host"),
			want: "resolve",
		},
		{
			name: "the source is reachable and the credentials are not",
			err:  errors.New(`failed to connect: FATAL: password authentication failed for user "vidra" (SQLSTATE 28P01)`),
			want: "reachable",
		},
		{
			name: "an error with nothing recognisable in it earns no guess",
			err:  errors.New("something nobody has seen before"),
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sourceDialAdvice(tc.err)
			if tc.want == "" {
				if got != "" {
					t.Errorf("advice = %q, want none — a wrong guess sends an operator to the wrong machine", got)
				}
				return
			}
			if !strings.Contains(strings.ToLower(got), tc.want) {
				t.Errorf("advice = %q, want it to mention %q", got, tc.want)
			}
			// The advice is a fixed sentence, not an echo: it is persisted on the
			// run row and shown to admins, and the error it was derived from
			// carries whatever the operator typed into --source-dsn.
			for _, echoed := range []string{"/tmp/.s.PGSQL", "SQLSTATE", "203.0.113.9", "peertube.invalid"} {
				if strings.Contains(got, echoed) {
					t.Errorf("the advice echoes the error it was given (%q): %q", echoed, got)
				}
			}
		})
	}
}
