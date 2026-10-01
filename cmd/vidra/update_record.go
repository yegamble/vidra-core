package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// `vidra update` pins each component at the tag releases/<tag>.json (meta repo)
// says it was RELEASED at, not one tag in all three keys: v0.7.4 and v0.7.5
// re-released vidra-core ALONE, so v0.7.5 everywhere named a vidra-user image
// that has never existed (deploy/pin-release.sh fixed this on 2026-09-20).
//
// Semantics are release-mapping.py's `resolve`, read with encoding/json: a usable
// record decides the pairing; NO USABLE RECORD IS NOT A REFUSAL (most releases
// are uniform, and the published-release check still refuses a guess naming an
// image nobody built); a record that names another release, pairs something
// NEWER than itself or nothing AT itself is ignored. validate()'s digest checks
// are not copied: deploy.sh runs the full checker over whatever this pins.

// resolvePairing is key -> tag for a release, and notes explaining a fallback.
func resolvePairing(root, target string) (map[string]string, []string) {
	body, err := os.ReadFile(filepath.Join(root, "releases", target+".json"))
	why := "this tree has no releases/" + target + ".json"
	if err == nil {
		pins, perr := decodePairing(body, target)
		if perr == nil {
			return pins, nil
		}
		why = fmt.Sprintf("this tree's releases/%s.json is unusable (%v)", target, perr)
	}
	uniform := make(map[string]string, len(updateComponents))
	for _, c := range updateComponents {
		uniform[c.key] = target
	}
	return uniform, []string{why + ", so the pairing is a GUESS: ALL THREE components are pinned at " + target +
		", which is right only if it moved every component. If it re-released one alone the others' images do not exist and the update stops before changing anything — `git pull` the meta repository so the tree carries the record"}
}

// decodePairing is the record as {env key: tag}, or why it cannot decide.
func decodePairing(body []byte, release string) (map[string]string, error) {
	var rec struct {
		Release    string `json:"release"`
		Components map[string]struct {
			Tag string `json:"tag"`
		} `json:"components"`
	}
	if err := json.Unmarshal(body, &rec); err != nil {
		return nil, fmt.Errorf("not JSON")
	}
	relV, ok := parseReleaseTag(release)
	if rec.Release != release || !ok {
		return nil, fmt.Errorf("it names release %q, not %s", rec.Release, release)
	}
	pins := make(map[string]string, len(updateComponents))
	atRelease := false
	for _, c := range updateComponents {
		tag := rec.Components[c.name].Tag
		v, ok := parseReleaseTag(tag)
		if !ok {
			return nil, fmt.Errorf("components.%s.tag %q is not a vMAJOR.MINOR.PATCH tag", c.name, tag)
		}
		if relV.less(v) {
			return nil, fmt.Errorf("%s at %s is NEWER than the release it claims to be", c.name, tag)
		}
		atRelease = atRelease || v == relV
		pins[c.key] = tag
	}
	if !atRelease {
		return nil, fmt.Errorf("none of its components is at %s", release)
	}
	return pins, nil
}

// missingNeeds names each unpublished sibling at the tag the PAIRING asked for,
// which is not the release's own tag for a core-only release.
func (p updatePlan) missingNeeds() string {
	var out []string
	for _, c := range updateComponents {
		for _, m := range p.missing {
			if m == c.repo {
				out = append(out, fmt.Sprintf("%s/%s %s", p.owner, c.repo, p.pins[c.key]))
			}
		}
	}
	return strings.Join(out, " and ")
}

// isBundleTree is deploy/lib.sh's is_bundle_tree, condition for condition:
// vidra-bundle.manifest (written only by make-bundle.sh) and no vidra-core/.git.
func isBundleTree(root string) bool {
	if _, err := os.Stat(filepath.Join(root, "vidra-bundle.manifest")); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(root, coreRepo, ".git"))
	return err != nil
}

// updateBundle is `vidra update` on a bundle tree. Everything this command does
// on a git checkout (find the newest release, rewrite the tags, deploy) is wrong
// here: a bundle takes a release's compose files and scripts only by unpacking
// its archive, so pinning tags alone would run new images on an old tree. The
// operation that is right is deploy/pin-release.sh's — it resolves the pairing,
// downloads the archive, verifies its checksum BEFORE unpacking, installs it and
// writes the three tags — so this hands over and gets out of the way, like the
// five wrapped scripts.
//
// ORDER IS THE INVARIANT, as it was when this only refused: nothing here reads
// the env file, asks GitHub, snapshots or runs git. That is why the flag checks
// below look at the parsed flags only, and why the missing-script fallback is
// the old refusal, with its Nothing-was-changed promise still true.
//
// Flags pin-release.sh has no equivalent for are refused, never dropped. It
// pins a NAMED release and then stops: it has no dry run (--check would pin), no
// confirmation to skip and no deploy to arm a rollback for (--yes and
// --no-rollback would read as a deploy that never ran), and it does not look up
// the newest release (that discovery is the GitHub call it owns; --tag it is).
func updateBundle(s streams, dep deployment, uf updateFlags) error {
	script := filepath.Join(dep.root, "deploy", "pin-release.sh")
	// Run through bash like every other wrapped script (exec.go), so an unpack
	// that dropped the exec bit is not a reason to refuse.
	if info, err := os.Stat(script); err != nil || !info.Mode().IsRegular() {
		return bundleRefusal(dep.root)
	}
	for _, f := range []struct {
		set  bool
		name string
	}{{uf.check, "--check"}, {uf.yes, "--yes"}, {uf.noRollback, "--no-rollback"}} {
		if f.set {
			return fmt.Errorf("update: %s is not supported on a bundle tree: deploy/pin-release.sh pins a named release and stops, with no dry run, no confirmation and no deploy of its own (./deploy/deploy.sh is the next, separate command). Nothing was changed", f.name)
		}
	}
	if uf.tag == "" {
		return fmt.Errorf("update: a bundle tree needs --tag vX.Y.Z: deploy/pin-release.sh pins the release you name and does not look up the newest. Nothing was changed")
	}
	fmt.Fprintf(s.out, "vidra update — %s is a release bundle: running deploy/pin-release.sh %s (downloads, verifies and installs it, then pins the tags; it does not deploy)\n", dep.root, uf.tag)
	return theRunner.Passthrough(dep.bash(script, uf.tag), s)
}

// bundleRefusal is the fallback when a bundle tree has no
// deploy/pin-release.sh (a bundle cut before it shipped, or a damaged tree). It
// names the manual procedure, not a command.
func bundleRefusal(root string) error {
	return fmt.Errorf("update: %s is an unpacked release BUNDLE (vidra-bundle.manifest is present and %s/ has no git history) and has no deploy/pin-release.sh, which is what upgrades a bundle tree, so `vidra update` cannot do it. Nothing was changed. Upgrade it by hand: download the release's vidra-bundle_<tag>.tar.gz, verify it against the release's SHA256SUMS and unpack it over the tree, set the three VIDRA_*_TAG keys to the tags that release's releases/<tag>.json pairs (they differ for a core-only release), then run deploy/deploy.sh — the steps are at https://vidra.yosef.app/docs/install/upgrading#upgrade-a-bundle-tree",
		root, coreRepo)
}
