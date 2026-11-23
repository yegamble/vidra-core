package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// The installed deployment owns both the on-disk format and its interpreter.
func maintenanceCommand(dep deployment, helper string, args ...string) (execSpec, error) {
	path := filepath.Join(dep.root, "deploy", helper)
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return execSpec{}, fmt.Errorf("this installation needs deploy/%s. Upgrade its deployment tree to a release containing the maintenance helpers", helper)
	}
	return execSpec{Path: "python3", Args: append([]string{path}, args...), Dir: dep.root, Env: dep.env}, nil
}

func runBackups(s streams, args []string) error {
	f, err := parseWrapperFlags("backups", args)
	if err != nil {
		return err
	}
	if f.help {
		fmt.Fprintln(s.out, "usage: vidra backups [-C dir] [--env file] [list | create | restore ID | retire ID | web] [options]\nRestore and retire preview the selected snapshot. Add --apply to execute.")
		return nil
	}
	dep, _, err := resolve("backups", f, "deploy.sh")
	if err != nil {
		return err
	}
	helper := "snapshots.py"
	rest := f.rest
	if len(rest) == 0 {
		rest = []string{"list"}
	} else if rest[0] == "web" {
		helper = "snapshot_ui.py"
		rest = rest[1:]
	}
	cmd, err := maintenanceCommand(dep, helper, rest...)
	if err != nil {
		return err
	}
	return theRunner.Passthrough(cmd, s)
}

func sourceUpdate(s streams, dep deployment, uf updateFlags) error {
	if uf.noRollback {
		return fmt.Errorf("--no-rollback is unavailable for a source build. Source upgrades use a full recovery snapshot")
	}
	args := []string{"apply"}
	if uf.check {
		args = []string{"check"}
	} else if !uf.yes {
		args = append(args, "--confirm")
	}
	if uf.tag != "" {
		args = append(args, "--tag", uf.tag)
	}
	cmd, err := maintenanceCommand(dep, "source-update.py", args...)
	if err != nil {
		return err
	}
	return theRunner.Passthrough(cmd, s)
}

// Match deploy/snapshot_lock.py so a restore cannot race an image update
// between its pre-upgrade snapshot and the change to the application's pins.
func lockImageUpdate(root string) (*os.File, error) {
	dir := filepath.Join(root, "backups")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, ".vidra-maintenance.lock"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("could not lock this installation for the update (another recovery or upgrade may be active): %w", err)
	}
	return file, nil
}

func snapshotBeforePin(dep deployment, s streams) error {
	if dep.maintenanceLock == nil {
		return nil
	}
	cmd, err := maintenanceCommand(dep, "snapshots.py", "create", "--reason", "pre-upgrade")
	if err != nil {
		return err
	}
	// ExtraFiles[0] is descriptor 3 in the child. The Python helper checks
	// its inode before sharing this exact open-file description's flock.
	cmd.extraFiles = []*os.File{dep.maintenanceLock}
	cmd.Env = append(append([]string(nil), cmd.Env...), "VIDRA_MAINTENANCE_LOCK_FD=3")
	var id strings.Builder
	s.out = io.MultiWriter(s.out, &id)
	if err = theRunner.Passthrough(cmd, s); err != nil {
		return err
	}
	snapshot := strings.TrimSpace(id.String())
	if _, err = hex.DecodeString(snapshot); len(snapshot) != 64 || err != nil {
		return fmt.Errorf("snapshot command returned no full snapshot ID. Upgrade stopped before pinning")
	}
	fmt.Fprintf(s.err, "From %s, preview recovery with vidra backups restore %s\n", dep.root, snapshot)
	return nil
}
