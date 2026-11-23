package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSourceUpdateUsesRecordedReleaseHelper(t *testing.T) {
	for _, tc := range []struct{ args, want []string }{
		{[]string{"--source", "--check", "--tag", "v1.2.3"}, []string{"check", "--tag", "v1.2.3"}},
		{[]string{"--source"}, []string{"apply", "--confirm"}},
		{[]string{"--source", "--yes"}, []string{"apply"}},
	} {
		st := newUpdateStage(t)
		write(t, filepath.Join(st.dir, "deploy", "source-update.py"), "# helper")
		if err := st.run(tc.args...); err != nil {
			t.Fatal(err)
		}
		c := st.runner.only(t)
		if c.Path != "python3" || filepath.Base(c.script()) != "source-update.py" || !slices.Equal(c.tail(), tc.want) {
			t.Fatalf("unexpected source command %s %v", c.Path, c.Args)
		}
	}
}

func TestBackupsUsesTheInstalledManager(t *testing.T) {
	for _, tc := range []struct {
		helper     string
		args, want []string
	}{
		{"snapshots.py", nil, []string{"list"}},
		{"snapshots.py", []string{"restore", "abcd"}, []string{"restore", "abcd"}},
		{"snapshot_ui.py", []string{"web", "--port", "9005"}, []string{"--port", "9005"}},
	} {
		f := swapRunner(t, &fakeRunner{})
		dir := fakeDeployment(t, defaultEnv)
		write(t, filepath.Join(dir, "deploy", tc.helper), "# helper")
		if err := newHarness(t).run(append([]string{"backups", "-C", dir}, tc.args...)...); err != nil {
			t.Fatal(err)
		}
		c := f.only(t)
		if c.Path != "python3" || filepath.Base(c.script()) != tc.helper || !slices.Equal(c.tail(), tc.want) {
			t.Fatalf("unexpected backup command %s %v", c.Path, c.Args)
		}
	}
}

func TestImageUpdateRefusesToPinWhenConfiguredSnapshotFails(t *testing.T) {
	for _, bundle := range []bool{false, true} {
		st := newUpdateStage(t)
		args := []string{"--yes"}
		if bundle {
			st.makeBundle()
			st.installPinRelease()
			args = []string{"--tag", "v0.3.0"}
		}
		before := st.envFile()
		st.runner.environ = append(st.runner.environ, "RESTIC_REPOSITORY=/backup/example")
		write(t, filepath.Join(st.dir, "deploy", "snapshots.py"), "# helper")
		st.runner.onPassthrough = func(c execSpec, s streams) error {
			if c.Path != "python3" || !slices.Equal(c.tail(), []string{"create", "--reason", "pre-upgrade"}) {
				t.Fatalf("unexpected pre-snapshot mutation %v", c.Args)
			}
			return errors.New("snapshot fixture failed")
		}
		if err := st.run(args...); err == nil || !strings.Contains(err.Error(), "snapshot fixture failed") {
			t.Fatalf("expected snapshot refusal, got %v", err)
		}
		if st.envFile() != before {
			t.Fatal("failed snapshot changed deployment pins")
		}
	}
}

func TestImageUpdateSnapshotsOldPinsUnderSharedLock(t *testing.T) {
	st := newUpdateStage(t)
	st.runner.environ = append(st.runner.environ, "RESTIC_REPOSITORY=/backup/example")
	write(t, filepath.Join(st.dir, "deploy", "snapshots.py"), "# helper")
	var captured bool
	st.runner.onPassthrough = func(c execSpec, s streams) error {
		if c.Path == "python3" {
			if len(c.extraFiles) != 1 || !strings.Contains(st.envFile(), "VIDRA_CORE_TAG=v0.2.0") {
				t.Fatal("snapshot has no shared lock or sees new tags")
			}
			if v, _ := c.envValue("VIDRA_MAINTENANCE_LOCK_FD"); v != "3" {
				t.Fatal("snapshot fd missing")
			}
			if lock, err := lockImageUpdate(st.dir); err == nil {
				lock.Close()
				t.Fatal("concurrent upgrade was allowed")
			}
			captured = true
			fmt.Fprintln(s.out, strings.Repeat("a", 64))
		} else if !captured {
			t.Fatal("deployed before snapshot")
		}
		return nil
	}
	if err := st.run("--yes"); err != nil {
		t.Fatal(err)
	}
	if !captured {
		t.Fatal("no configured snapshot")
	}
	lock, err := lockImageUpdate(st.dir)
	if err != nil {
		t.Fatal("lock leaked after update")
	}
	defer lock.Close()
	st.runner.environ = nil // a separate operator shell without backup credentials
	if err := st.run("--yes"); err == nil || !strings.Contains(err.Error(), "could not lock") {
		t.Fatalf("an unconfigured caller escaped the recovery lock: %v", err)
	}
}
