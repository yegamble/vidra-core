package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/blobverify"
)

// Everything below stops before config.Load(), so these run with no database,
// no bucket and no environment — which is the point: a usage mistake must be
// reported as a usage mistake, not as "the database is unreachable".
func TestVerifyBlobsUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		errHas string
	}{
		{name: "an unknown flag", args: []string{"--fix"}, errHas: "flag provided but not defined"},
		{name: "a positional argument", args: []string{"everything"}, errHas: "takes no positional arguments"},
		{name: "a zero timeout", args: []string{"--timeout=0"}, errHas: "--timeout must be positive"},
		{name: "a negative timeout", args: []string{"--timeout=-5m"}, errHas: "--timeout must be positive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			if code := runVerifyBlobs(tc.args, &out, &errBuf); code != 1 {
				t.Fatalf("exit = %d, want 1", code)
			}
			if !strings.Contains(errBuf.String(), tc.errHas) {
				t.Errorf("stderr = %q, want it to mention %q", errBuf.String(), tc.errHas)
			}
			if out.Len() != 0 {
				t.Errorf("a refused invocation wrote to stdout: %q", out.String())
			}
		})
	}
}

// The --json document is what a machine reads; it must be one object with the
// report's fields flattened alongside the run's own two facts, and the exit
// code must be in it. A consumer that captured stdout and lost the process
// status still has to be able to tell what this run concluded.
func TestVerifyBlobsJSONShape(t *testing.T) {
	doc := verifyBlobsJSON{
		Report: blobverify.Report{
			Checked:     3,
			Present:     2,
			Missing:     1,
			MissingKeys: []string{"web-videos/gone.mp4"},
		},
		StorageMigrationActive: true,
		ExitCode:               exitInconsistent,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"checked", "present", "missing", "missing_keys", "storage_migration_active", "exit_code"} {
		if _, ok := got[key]; !ok {
			t.Errorf("the JSON document has no %q field: %s", key, raw)
		}
	}
	if got["exit_code"].(float64) != float64(exitInconsistent) {
		t.Errorf("exit_code = %v, want %d", got["exit_code"], exitInconsistent)
	}
}

// The exit-code contract deploy/restore.sh reads. 3 is deliberately not 1: a
// restore must be able to tell "verified, and it is wrong" from "could not
// verify", and it continues in both cases.
func TestVerifyBlobsExitCodeContract(t *testing.T) {
	if exitInconsistent != 3 {
		t.Fatalf("exitInconsistent = %d — deploy/restore.sh branches on 3", exitInconsistent)
	}
	clean := blobverify.Report{Checked: 10, Present: 10}
	if !clean.Consistent() {
		t.Error("a store with everything present is not consistent")
	}
	for name, rep := range map[string]blobverify.Report{
		"missing":    {Missing: 1},
		"mismatched": {Mismatched: 1},
		"incomplete": {Incomplete: 1},
		"unreadable": {Errors: 1},
	} {
		if rep.Consistent() {
			t.Errorf("%s did not make the run inconsistent", name)
		}
	}
	// Known-missing rows are reported, never fatal — see blobverify.Report.Problems.
	if !(blobverify.Report{KnownMissing: 7}).Consistent() {
		t.Error("already-recorded dangling rows failed the run")
	}
}

// The usage line main() prints for an unknown subcommand has to name this one,
// or an operator who mistypes it is told only about `migrate`.
func TestVerifyBlobsIsInTheUsageLine(t *testing.T) {
	if !strings.HasPrefix(verifyBlobsUsage, "verify-blobs") {
		t.Fatalf("verifyBlobsUsage = %q", verifyBlobsUsage)
	}
	for _, flagName := range []string{"--hash", "--deep", "--json", "--timeout"} {
		if !strings.Contains(verifyBlobsUsage, flagName) {
			t.Errorf("the usage line omits %s: %q", flagName, verifyBlobsUsage)
		}
	}
}
