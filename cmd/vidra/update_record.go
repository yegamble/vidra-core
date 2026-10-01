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

// bundleRefusal precedes every read, request and write: a bundle tree has no
// history to check migrations against and takes a release's compose files and
// deploy scripts only by unpacking its bundle, so pinning tags alone would run
// new images on an old tree. It names the manual procedure, not a command.
func bundleRefusal(root string) error {
	return fmt.Errorf("update: %s is an unpacked release BUNDLE (vidra-bundle.manifest is present and %s/ has no git history), and `vidra update` does not support a bundle tree yet. Nothing was changed. Upgrade it by hand: unpack the release's vidra-bundle_<tag>.tar.gz over the tree, set the three VIDRA_*_TAG keys to the tags that release's releases/<tag>.json pairs (they differ for a core-only release), then run deploy/deploy.sh — deploy/README.md has the steps, under \"On a bundle tree there is no `git pull`\"",
		root, coreRepo)
}
