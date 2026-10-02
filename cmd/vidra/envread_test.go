package main

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
)

// After `sudo ./deploy/provision.sh` the tree belongs to the vidra user and
// env/production.env is 0600, so the sudo user who ran the installer gets EACCES
// on `vidra claim` and `vidra doctor`. "could not be read" with the cause
// dropped sent them to chmod a secrets file; the answer is to run as its owner.
func TestEnvReadPermissionNamesTheOwnerAndTheFix(t *testing.T) {
	old := lookupEnvOwner
	t.Cleanup(func() { lookupEnvOwner = old })
	lookupEnvOwner = func(string) envOwner { return envOwner{name: "vidra", known: true} }

	dep := deployment{envFile: "env/production.env", envPath: "/srv/vidra/env/production.env"}
	err := dep.envReadError(&fs.PathError{Op: "open", Path: dep.envPath, Err: fs.ErrPermission})
	for _, want := range []string{"env/production.env", "belongs to vidra", "sudo -u vidra vidra <the same command>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Errorf("the cause was dropped: %v", err)
	}
}

// A file the caller already owns has a different fix: sudo -u would change
// nothing, the mode is what denies it.
func TestEnvReadPermissionOwnedByCallerSaysChmod(t *testing.T) {
	old := lookupEnvOwner
	t.Cleanup(func() { lookupEnvOwner = old })
	lookupEnvOwner = func(string) envOwner { return envOwner{name: "me", known: true, self: true} }

	dep := deployment{envFile: "env/production.env", envPath: "/x/env/production.env"}
	err := dep.envReadError(&fs.PathError{Op: "open", Path: dep.envPath, Err: fs.ErrPermission})
	if !strings.Contains(err.Error(), "chmod") || strings.Contains(err.Error(), "sudo -u") {
		t.Errorf("want a chmod hint and no sudo -u advice: %v", err)
	}
}

// When even the owner cannot be learned (a parent directory the caller cannot
// traverse), the advice still names the provisioned user instead of guessing.
func TestEnvReadPermissionUnknownOwnerStillHelps(t *testing.T) {
	old := lookupEnvOwner
	t.Cleanup(func() { lookupEnvOwner = old })
	lookupEnvOwner = func(string) envOwner { return envOwner{} }

	dep := deployment{envFile: "env/production.env", envPath: "/x/env/production.env"}
	err := dep.envReadError(&fs.PathError{Op: "open", Path: dep.envPath, Err: fs.ErrPermission})
	for _, want := range []string{"permission denied", "sudo -u vidra vidra <the same command>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// Every other failure keeps its cause: an EISDIR or EIO is not a permissions
// problem and must not be dressed up as one.
func TestEnvReadOtherErrorsKeepTheirCause(t *testing.T) {
	cause := errors.New("input/output error")
	dep := deployment{envFile: "env/production.env", envPath: "/x/env/production.env"}
	err := dep.envReadError(cause)
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "input/output error") {
		t.Errorf("the cause is gone: %v", err)
	}
	if strings.Contains(err.Error(), "sudo") {
		t.Errorf("a non-permission error suggested sudo: %v", err)
	}
}

// The real stat path: a file this process just created is owned by this user.
func TestLookupEnvOwnerOnARealFile(t *testing.T) {
	path := t.TempDir() + "/e.env"
	write(t, path, "A=1\n")
	got := statEnvOwner(path)
	if !got.known || !got.self || got.name == "" {
		t.Errorf("statEnvOwner(own file) = %+v, want known, self, named", got)
	}
	if statEnvOwner(path + ".missing").known {
		t.Error("a missing file reported an owner")
	}
}
