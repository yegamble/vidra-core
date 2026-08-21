package doctor

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/vidra/vidra-core/internal/setup"
	"github.com/vidra/vidra-core/internal/storage"
)

// checkObjectStorage makes one authenticated call against the bucket the api
// would write uploads into.
//
// It is HeadBucket and nothing else. A diagnostic must not create the bucket it
// cannot find (that turns a typo in STORAGE_S3_BUCKET into a new, empty,
// silently-wrong store), and it must not write an object either — the point is
// to prove the credentials, the endpoint, the region and the bucket name agree
// with each other, which one head request does.
func checkObjectStorage(ctx context.Context, s *state) []Finding {
	if s.envErr != nil {
		return []Finding{skipf(fmt.Sprintf("the env file could not be read (%s)", s.envErr))}
	}
	backend := strings.ToLower(s.value("STORAGE_BACKEND"))
	if backend != "s3" {
		// Not a skip: the check ran, and found there is no object store in this
		// deployment to reach. A permanent ⚠ on a correctly configured
		// local-storage instance is how a report trains people to stop reading it.
		return []Finding{okf(fmt.Sprintf("STORAGE_BACKEND=%s — media lives in the media_data volume, so there is no object store to reach (make sure that volume is in your backups)", orDefault(backend, "local")))}
	}
	cfg := storage.S3Config{
		Endpoint:       s.value("STORAGE_S3_ENDPOINT"),
		Bucket:         s.value("STORAGE_S3_BUCKET"),
		AccessKey:      s.value("STORAGE_S3_ACCESS_KEY"),
		SecretKey:      s.value("STORAGE_S3_SECRET_KEY"),
		Region:         s.value("STORAGE_S3_REGION"),
		UseSSL:         !isFalseish(s.value("STORAGE_S3_USE_SSL")),
		ForcePathStyle: setup.IsTrue(s.value("STORAGE_S3_FORCE_PATH_STYLE")),
	}
	exists, err := s.opt.Prober.CheckBucket(ctx, cfg)
	switch {
	case err != nil:
		return []Finding{failf(
			fmt.Sprintf("the object store at %s could not be reached or would not authenticate: %s", cfg.Endpoint, reachSummary(err)),
			"check STORAGE_S3_ENDPOINT (host only, no scheme), STORAGE_S3_REGION and the access/secret key in "+s.envRel+". Every upload fails while this does, and the failure surfaces to users as a stuck upload rather than an error")}
	case !exists:
		return []Finding{failf(
			fmt.Sprintf("the credentials work, but the bucket %q does not exist at %s", cfg.Bucket, cfg.Endpoint),
			"create the bucket (Vidra does not create it for you in production, deliberately), or fix STORAGE_S3_BUCKET in "+s.envRel+" — a typo here reads as an empty library rather than an error")}
	default:
		return []Finding{okf(fmt.Sprintf("the bucket %q at %s answers an authenticated request", cfg.Bucket, cfg.Endpoint))}
	}
}

// checkObjectRetention answers a question an operator cannot see from their
// bucket browser and only finds out about on an invoice: does deleting an object
// here actually free the bytes?
//
// On a VERSIONED bucket it does not. A delete writes a delete marker (Backblaze
// calls it a hide marker) and the previous version keeps existing and keeps
// billing. That is not a corner case for Vidra — it is the normal path:
//
//   - every resumable upload writes its chunks to uploads/<session>/* and
//     deletes them once the original is assembled, so a 2 GB upload is stored
//     twice and billed twice, permanently;
//   - re-transcoding a video clears the previous generation's HLS tree first;
//   - internal/mediagc's whole job is deleting orphans, and on such a bucket it
//     reclaims exactly nothing.
//
// Backblaze B2 buckets are versioned BY DEFAULT, which is what makes this worth
// a check rather than a documentation line. A lifecycle rule that expires
// non-current versions fixes it, and B2 additionally warns that accumulating
// many versions of one object degrades listing and delete performance.
func checkObjectRetention(ctx context.Context, s *state) []Finding {
	if s.envErr != nil {
		return []Finding{skipf(fmt.Sprintf("the env file could not be read (%s)", s.envErr))}
	}
	if strings.ToLower(s.value("STORAGE_BACKEND")) != "s3" {
		return []Finding{okf("media lives on local disk, where a delete frees the bytes immediately — object versioning does not apply")}
	}
	cfg := storage.S3Config{
		Endpoint:       s.value("STORAGE_S3_ENDPOINT"),
		Bucket:         s.value("STORAGE_S3_BUCKET"),
		AccessKey:      s.value("STORAGE_S3_ACCESS_KEY"),
		SecretKey:      s.value("STORAGE_S3_SECRET_KEY"),
		Region:         s.value("STORAGE_S3_REGION"),
		UseSSL:         !isFalseish(s.value("STORAGE_S3_USE_SSL")),
		ForcePathStyle: setup.IsTrue(s.value("STORAGE_S3_FORCE_PATH_STYLE")),
	}
	retention, err := s.opt.Prober.CheckBucketRetention(ctx, cfg)
	if err != nil {
		// The reachability check above already reports an unreachable store; a
		// second failure line for the same cause is noise.
		return []Finding{skipf(fmt.Sprintf("the bucket %q would not report its versioning or lifecycle configuration (%s)", cfg.Bucket, reachSummary(err)))}
	}

	const fix = "set a lifecycle rule on the bucket that expires NON-CURRENT versions (Backblaze calls this `daysFromHidingToDeleting`; on the S3 API it is NoncurrentVersionExpiration). A few days is plenty — Vidra never reads a superseded version. Without it every upload is billed roughly twice forever, and `vidra` media garbage collection frees nothing"

	reclaims, known := retention.ReclaimsOnDelete()
	switch {
	case !known:
		return []Finding{warnf(
			fmt.Sprintf("the bucket %q did not fully answer whether it keeps previous versions, so it is unknown whether deletes reclaim space", cfg.Bucket),
			"check in your provider's console whether the bucket has versioning on. "+fix)}
	case reclaims && !retention.VersioningEnabled:
		return []Finding{okf(fmt.Sprintf("versioning is off on %q — deletes and overwrites reclaim space immediately", cfg.Bucket))}
	case reclaims:
		return []Finding{okf(fmt.Sprintf("%q is versioned, and lifecycle rule %q expires non-current versions — deleted and superseded objects are reclaimed", cfg.Bucket, retention.NoncurrentExpiryRule))}
	default:
		return []Finding{warnf(
			fmt.Sprintf("%q keeps previous versions and has no lifecycle rule expiring them, so nothing Vidra deletes is ever reclaimed: upload chunks removed after assembly, superseded HLS trees, and everything media garbage collection sweeps all keep billing", cfg.Bucket),
			fix)}
	}
}

// checkBucketOwnership looks for the object that says this bucket belongs to
// this install (storage.OwnerMarkerKey). Its absence is what stands between a
// shared or pre-populated bucket and a daily sweep that deletes every object no
// database row references.
//
// It reports PRESENCE and nothing more. Comparing the marker against the
// install's own identity would need the database — a `vidra doctor` that read
// the identity table would be answering a different question with a second
// connection, and it would still be the api's answer that governs, since the api
// resolves ownership at boot and logs what it found. So: found is ok, absent is
// a warning naming the adoption endpoint, and a bucket carrying somebody else's
// UUID reads as ok here and as a refusal in the api's log — which is why the
// fix text sends the operator to that log rather than leaving them to compare
// UUIDs by eye.
func checkBucketOwnership(ctx context.Context, s *state) []Finding {
	if s.envErr != nil {
		return []Finding{skipf(fmt.Sprintf("the env file could not be read (%s)", s.envErr))}
	}
	if strings.ToLower(s.value("STORAGE_BACKEND")) != "s3" {
		return []Finding{okf("media lives on local disk, which this instance populated itself — there is no shared bucket to mistake for its own, so the ownership marker does not apply")}
	}
	cfg := storage.S3Config{
		Endpoint:       s.value("STORAGE_S3_ENDPOINT"),
		Bucket:         s.value("STORAGE_S3_BUCKET"),
		AccessKey:      s.value("STORAGE_S3_ACCESS_KEY"),
		SecretKey:      s.value("STORAGE_S3_SECRET_KEY"),
		Region:         s.value("STORAGE_S3_REGION"),
		UseSSL:         !isFalseish(s.value("STORAGE_S3_USE_SSL")),
		ForcePathStyle: setup.IsTrue(s.value("STORAGE_S3_FORCE_PATH_STYLE")),
	}
	found, _, err := s.opt.Prober.CheckBucketMarker(ctx, cfg)
	switch {
	case err != nil:
		// The reachability check above already reports an unreachable store in
		// full; a second failure line for the same cause is noise.
		return []Finding{skipf(fmt.Sprintf("the ownership marker %q in %q could not be read (%s)", storage.OwnerMarkerKey, cfg.Bucket, reachSummary(err)))}
	case !found:
		return []Finding{warnf(
			fmt.Sprintf("%q carries no ownership marker (%s), so this instance has not established that the bucket is its own — media garbage collection will report what it would delete and delete nothing", cfg.Bucket, storage.OwnerMarkerKey),
			"if this bucket is yours, adopt it once: POST /api/v1/admin/media/gc/adopt-bucket as an admin, which writes the marker and re-enables the sweep. If it is NOT yours — a shared bucket, or the destination of a migration in progress — leave it: that refusal is the point. A bucket the api CREATED, or one that was empty at boot, is marked automatically, so seeing this on a working instance means it had objects in it before Vidra did")}
	default:
		return []Finding{okf(fmt.Sprintf("%q carries an ownership marker (%s), so media garbage collection is allowed to delete from it. If the api's log says otherwise at boot, the marker belongs to a DIFFERENT install and the two must not share this bucket", cfg.Bucket, storage.OwnerMarkerKey))}
	}
}

// checkSMTP proves there is a relay at the address the api will hand password
// resets and email verifications to. It dials and reads the greeting; it never
// sends, and it never authenticates — a diagnostic that logs into the mail relay
// on every run is a diagnostic that eventually gets the account rate-limited.
func checkSMTP(ctx context.Context, s *state) []Finding {
	if s.envErr != nil {
		return []Finding{skipf(fmt.Sprintf("the env file could not be read (%s)", s.envErr))}
	}
	host := s.value("SMTP_HOST")
	if !setup.IsTrue(s.value("MAIL_ENABLED")) || host == "" {
		return []Finding{okf("mail is off (MAIL_ENABLED is not true, or SMTP_HOST is blank) — password reset and email verification are unavailable, which is a deliberate configuration and not a fault")}
	}
	port := s.value("SMTP_PORT")
	if port == "" {
		port = "587"
	}
	if _, err := strconv.Atoi(port); err != nil {
		return []Finding{failf(
			fmt.Sprintf("SMTP_PORT is %q, which is not a port number", truncate(port, 20)),
			"set SMTP_PORT in "+s.envRel+" (587 with STARTTLS is the usual answer)")}
	}
	addr := net.JoinHostPort(host, port)
	banner, err := s.opt.Prober.CheckSMTP(ctx, addr)
	switch {
	case err != nil:
		return []Finding{failf(
			fmt.Sprintf("no SMTP relay answered at %s: %s", addr, reachSummary(err)),
			"check SMTP_HOST/SMTP_PORT in "+s.envRel+" and that this host's egress on that port is open — most cloud providers block outbound 25 outright, which is why 587 (or the provider's 2525) is the port to use. Every password reset silently fails while this does")}
	case !strings.HasPrefix(strings.TrimSpace(banner), "220"):
		return []Finding{warnf(
			fmt.Sprintf("something answered at %s but its greeting was not an SMTP 220: %q", addr, truncate(banner, 80)),
			"confirm SMTP_HOST/SMTP_PORT name a mail relay and not, say, a proxy or a captive portal in front of one")}
	default:
		return []Finding{okf(fmt.Sprintf("the SMTP relay at %s answered %q", addr, truncate(banner, 60)))}
	}
}

// checkSearchService probes vidra-search from INSIDE the compose network,
// because in production it is reachable nowhere else: docker-compose.prod.yml
// resets its `ports:` outright, deliberately — it is HMAC-authenticated and
// called only by the api, never by a browser. So the probe is an exec into the
// running container, and a stack that is not up is a skip rather than a failure.
func checkSearchService(ctx context.Context, s *state) []Finding {
	running, why := s.containers(ctx)
	if why != "" {
		return []Finding{skipf("the search service publishes no host port in production, so it is probed from inside the stack, and the running containers could not be inspected (" + why + ")")}
	}
	c, ok := serviceContainer(running, "search")
	if !ok {
		return []Finding{skipf("the search service is not running on this host (it publishes no host port, so there is no other way to reach it)")}
	}
	args := s.composeArgs("exec", "-T", "search", "wget", "-qO-", "http://127.0.0.1:8080/healthz")
	out, err := s.opt.Host.Run(ctx, s.root, "docker", args...)
	if err != nil {
		return []Finding{skipf("docker is not on this host's PATH, so the search container could not be probed")}
	}
	if out.ExitCode != 0 {
		detail := fmt.Sprintf("the search service is running (%s) but does not answer /healthz on its own loopback", orDefault(c.Health, c.State))
		return []Finding{failf(detail,
			"read its log (`"+s.composeCommand("logs", "--tail=50", "search")+"`). Search failing does not take the site down — the api degrades to database queries — but discovery and suggestions are wrong until it is back")}
	}
	return []Finding{okf(fmt.Sprintf("the search service answers /healthz inside the compose network (%s)", orDefault(c.Health, c.State)))}
}

// checkFFmpeg looks for the binary every transcode shells out to. It is checked
// where it will be USED — inside the api container when that is running, on the
// host PATH only as a fallback — because a host with ffmpeg installed says
// nothing about an image built without it, and the two are routinely different.
//
// A ⚠ rather than a ✗: uploads still arrive and the site still serves. What
// stops is every transcode, and the symptom is a video stuck at "processing"
// with a job that retries forever.
func checkFFmpeg(ctx context.Context, s *state) []Finding {
	running, why := s.containers(ctx)
	if why == "" {
		if _, ok := serviceContainer(running, "api"); ok {
			args := s.composeArgs("exec", "-T", "api", "ffmpeg", "-version")
			out, err := s.opt.Host.Run(ctx, s.root, "docker", args...)
			if err == nil && out.ExitCode == 0 {
				return []Finding{okf("ffmpeg is present in the api container: " + firstLine(out.Stdout))}
			}
			if err == nil {
				return []Finding{warnf(
					"ffmpeg is not available inside the api container, so every transcode will fail",
					"that binary is baked into the image, so this means the wrong image is deployed. Check VIDRA_CORE_TAG in "+s.envRel+" and re-deploy")}
			}
		}
	}
	path, err := s.opt.Host.LookPath("ffmpeg")
	if err != nil {
		return []Finding{warnf(
			"ffmpeg is not on this host's PATH, and the api container was not available to check the one that matters",
			"transcodes run INSIDE the api container, so a missing host ffmpeg is only a problem for running the tools by hand. Bring the stack up and re-run to check the real one")}
	}
	return []Finding{warnf(
		"the api container is not running, so ffmpeg was only found on the host ("+path+")",
		"transcodes run inside the api container, not here — bring the stack up and re-run to check the binary that actually does the work")}
}

// isFalseish is setup.IsTrue's opposite for a knob whose DEFAULT is on:
// STORAGE_S3_USE_SSL is true unless the file says otherwise. It is its own
// function because "unset means true" cannot be expressed by negating a
// true-test — !IsTrue("") is true, which would turn SSL off on every file that
// does not mention it.
func isFalseish(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "n", "off":
		return true
	default:
		return false
	}
}
