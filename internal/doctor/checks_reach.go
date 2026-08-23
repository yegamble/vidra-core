package doctor

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/setup"
	"github.com/vidra/vidra-core/internal/storage"
)

// A NOTE ON THE internal/media IMPORT, WHICH IS AN EXCEPTION TO THIS PACKAGE'S
// OWN RULE. The extra-codec check below spells its two encoder names out rather
// than reading them from internal/media, because this package diagnoses a
// DEPLOYMENT — an env file and a container — and importing the media pipeline at
// build time to learn two strings would tie the diagnostic to the thing it
// diagnoses. media.VideoCodecEncoders() exists so a TEST-ONLY import can pin the
// duplication without creating it, and TestVideoEncoderKnobsMatchTheRegistry does
// exactly that.
//
// The hardware check imports it for real, and the trick above does not reach the
// case. What it needs is not two constants but an ALGORITHM and a four-row table:
// which encoders each backend names, which device nodes make it plausible, what
// the host must provide, and the exact sentence that offers the opt-in. A pinning
// test can compare two tables; it cannot keep a reimplemented availability rule
// honest. Mirroring here would be duplicating the feature, and the symptom of a
// mirror gone stale is a report that recommends a backend the api then refuses to
// boot on — which is worse than the coupling.

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

// videoEncoderKnobs maps the boot-baked extra-codec settings to the ffmpeg
// encoder each one needs.
//
// The names are spelled out rather than read from internal/media for the same
// reason internal/config spells the packager names out: this package diagnoses a
// DEPLOYMENT — an env file and a container — and importing the media pipeline to
// learn two string constants would tie a diagnostic to the thing it diagnoses.
// The authority they mirror is the codec registry in internal/media/codec.go;
// they have to be changed together, and the media package's own boot probe is
// what actually stops a mismatched deployment.
var videoEncoderKnobs = []struct{ envVar, encoder, codec string }{
	{"TRANSCODING_HEVC_ENABLED", "libx265", "HEVC/H.265"},
	{"TRANSCODING_AV1_ENABLED", "libsvtav1", "AV1"},
}

// checkVideoEncoders answers the question the extra-codec knobs raise: does the
// ffmpeg that will actually run the transcodes HAVE the encoder they ask for?
//
// It mirrors the boot-time probe the api makes of its own binary, and it is
// checked where it will be USED — inside the api container when that is running,
// on the host PATH only as a fallback — because a host ffmpeg built with libx265
// says nothing about the one in the image, and the two are routinely different.
//
// A ✗ rather than a ⚠ when an enabled codec's encoder is missing FROM THE
// CONTAINER: the api refuses to boot on it, so this is not a degradation, it is
// why the stack is down. Everything the HOST binary says is a ⚠ either way — see
// the switch below. A deployment with neither knob on gets a plain ✓: the
// H.264-only default is the correct configuration, not an absence of one, and a
// permanent ⚠ on it is how a report teaches people to stop reading it.
func checkVideoEncoders(ctx context.Context, s *state) []Finding {
	if s.envErr != nil {
		return []Finding{skipf(fmt.Sprintf("the env file could not be read (%s)", s.envErr))}
	}
	var want []struct{ envVar, encoder, codec string }
	for _, k := range videoEncoderKnobs {
		if setup.IsTrue(s.value(k.envVar)) {
			want = append(want, k)
		}
	}
	if len(want) == 0 {
		return []Finding{okf("only H.264 is enabled, which every client can play and every ffmpeg can encode — there is no extra encoder to check")}
	}
	asked := make([]string, 0, len(want))
	for _, k := range want {
		asked = append(asked, k.codec)
	}
	list, where, container, why := s.ffmpegEncoders(ctx)
	if why != "" {
		return []Finding{warnf(
			fmt.Sprintf("%s %s enabled, but the ffmpeg that would encode %s could not be asked what it supports (%s)",
				strings.Join(asked, " and "), plural(len(want), "is", "are"), plural(len(want), "it", "them"), why),
			"bring the stack up and re-run: the api refuses to boot when an enabled codec's encoder is missing, so this check is how you find that out before a deploy rather than after")}
	}
	// A HOST answer is only ever a ⚠, whichever way it comes out. Transcodes run
	// inside the api container, and this host's ffmpeg is a different build that
	// says nothing about the image's — so "the host has libx265" is not evidence
	// the deployment works, and "the host lacks libsvtav1" is not evidence it is
	// broken. Reporting either as a verdict is worse than reporting neither,
	// because it names the wrong binary with total confidence.
	hostFix := "transcodes run inside the api container, not here — bring the stack up and re-run to check the binary that actually does the work"
	var findings []Finding
	for _, k := range want {
		switch {
		case list[k.encoder] && container:
			findings = append(findings, okf(fmt.Sprintf("%s is enabled and %s has the %s encoder", k.codec, where, k.encoder)))
		case list[k.encoder]:
			findings = append(findings, warnf(
				fmt.Sprintf("%s is enabled and %s has the %s encoder, but that is not the ffmpeg that will run the transcodes", k.codec, where, k.encoder),
				hostFix))
		case container:
			findings = append(findings, failf(
				fmt.Sprintf("%s=true, but %s has no %q encoder", k.envVar, where, k.encoder),
				fmt.Sprintf("the api refuses to boot like this, so nothing is transcoding. Either deploy an ffmpeg built with %s or set %s=false in %s and restart",
					k.encoder, k.envVar, s.envRel)))
		default:
			findings = append(findings, warnf(
				fmt.Sprintf("%s=true and %s has no %q encoder — but the api container was not available, so this says nothing about the image", k.envVar, where, k.encoder),
				hostFix))
		}
	}
	return findings
}

// --- hardware transcoding (phase-3 item 7) -----------------------------------

// hwRenderNodeCandidates are the DRM render nodes this check looks for.
//
// A fixed list rather than a glob: Host is the read-only machine interface, it
// has no ReadDir, and adding one so a diagnostic can enumerate /dev/dri would be
// more surface than the check earns. renderD128 is the first node the kernel
// hands out and is the answer on every single-GPU host; the three after it cover
// the iGPU-plus-discrete-card machines where the interesting question is WHICH
// node, and past that an operator who has four GPUs knows what they have.
var hwRenderNodeCandidates = []string{
	"/dev/dri/renderD128", "/dev/dri/renderD129", "/dev/dri/renderD130", "/dev/dri/renderD131",
}

// checkHardwareTranscode reports what hardware video encoding is possible here,
// and never fails.
//
// It is INFORMATIONAL by construction, and that is the design rather than
// timidity. A deployment with no GPU is not misconfigured — CPU encoding is the
// default, works everywhere, and is what the ladder is budgeted for — so a ⚠ on
// every ordinary droplet is precisely how a report teaches people to stop reading
// it. What the check is for is the other direction: telling the operator who
// HAS the hardware that they are paying for a GPU their transcodes never touch,
// and telling the one who turned it on whether it can actually work.
//
// The device half is "plausibly", not "certainly", and the check says so where it
// matters. doctor reads the HOST's filesystem; the transcodes run in a container
// that only has /dev/dri if somebody mapped it in. That gap is the single most
// likely way this feature fails in production, so a deployment that has turned
// the knob on is told about it here rather than finding out one dead-lettered
// upload at a time.
func checkHardwareTranscode(ctx context.Context, s *state) []Finding {
	if s.envErr != nil {
		return []Finding{skipf(fmt.Sprintf("the env file could not be read (%s)", s.envErr))}
	}
	configured := s.value("TRANSCODING_HW")
	if configured == "" {
		configured = media.HardwareOff
	}
	on := configured != media.HardwareOff

	list, where, container, why := s.ffmpegEncoders(ctx)
	if why != "" {
		if !on {
			return []Finding{skipf("the ffmpeg that would encode could not be asked what it supports (" + why + "), so there is nothing to report about hardware encoding. CPU encoding is the default and needs no hardware at all")}
		}
		return []Finding{warnf(
			fmt.Sprintf("TRANSCODING_HW=%s, but the ffmpeg that would use it could not be asked what it supports (%s)", configured, why),
			"bring the stack up and re-run: the api refuses to boot when the chosen backend's encoder is missing, so this check is how you find that out before a deploy rather than after")}
	}

	// GOOS is deliberately left EMPTY, and this is the one place the difference
	// bites. runtime.GOOS here is the platform `vidra doctor` is running on; the
	// transcodes run inside a Linux container, so on a macOS deployment host the
	// two disagree and every answer derived from the wrong one is wrong — it would
	// rule VAAPI out as "linux-only" on the very deployment whose api container is
	// Linux and has h264_vaapi. The encoder set carries the platform anyway: a
	// Linux ffmpeg never has *_videotoolbox and a macOS one never has *_vaapi, so
	// the availability AND already excludes the impossible combinations, and it
	// excludes them by naming the encoder that is missing — which is the more
	// actionable half of the reason in any case.
	probe := media.HardwareProbe{Encoders: list, NVIDIA: s.hasNVIDIA()}
	for _, node := range hwRenderNodeCandidates {
		if _, err := s.opt.Host.Stat(node); err == nil {
			probe.RenderNodes = append(probe.RenderNodes, node)
		}
	}

	// A HOST answer is never a verdict about the CONTAINER — the rule the extra-
	// codec check above states, and it lands harder here. Both halves of the
	// availability AND are measured on the wrong side of the boundary: this host's
	// ffmpeg is a different build from the image's, and this host's /dev/dri may or
	// may not be inside the container. So a host-derived answer says what it is,
	// and the offer it makes is a lead rather than a finding.
	if !on {
		offer, ok := media.FirstAvailableHardware(probe)
		if !ok {
			if !container {
				return []Finding{okf("no hardware video encoder is usable according to " + where + " — but that is not the binary that encodes, so this is not a verdict about the deployment. Either way CPU encoding is the default, is what the ladder is budgeted for, and works on every host")}
			}
			return []Finding{okf("no hardware video encoder is usable here, so transcodes run on the CPU — which is the default, is what the ladder is budgeted for, and works on every host")}
		}
		if !container {
			return []Finding{okf(offer.Offer() + " — checked against " + where + ", which is NOT the ffmpeg that runs the transcodes. Bring the stack up and re-run to ask the api container's")}
		}
		return []Finding{okf(offer.Offer() + " (checked against " + where + ")")}
	}

	// The knob is on. Report whether it can work, and never harder than a ⚠: a
	// wrong value here stops the api booting, which the stack checks already say
	// far more loudly than a line in the reachability section could.
	for _, a := range media.DetectHardware(probe) {
		if a.Backend != configured {
			continue
		}
		if !a.Available {
			if !container {
				return []Finding{warnf(
					fmt.Sprintf("TRANSCODING_HW=%s, and according to %s %s — but that is not the ffmpeg that will run the transcodes, so this says nothing about the image", configured, where, a.Why),
					"bring the stack up and re-run to ask the api container's ffmpeg, which is the binary the api actually probes at boot")}
			}
			return []Finding{warnf(
				fmt.Sprintf("TRANSCODING_HW=%s, but %s", configured, a.Why),
				fmt.Sprintf("that backend needs %s. Set TRANSCODING_HW=off in %s to encode on the CPU — which always works — or fix the host and re-run. The api refuses to boot when the encoder is missing, so a stack that will not start is probably this",
					a.Requires, s.envRel))}
		}
		if !container {
			return []Finding{warnf(
				fmt.Sprintf("TRANSCODING_HW=%s, and %s has %s — but that is not the ffmpeg that will run the transcodes", configured, where, strings.Join(a.Encoders, " + ")),
				"bring the stack up and re-run: the api container's ffmpeg is a different build, and it is the one whose missing encoder stops the api booting")}
		}
		detail := fmt.Sprintf("TRANSCODING_HW=%s and %s has %s", configured, where, strings.Join(a.Encoders, " + "))
		if a.Device != "" {
			// Said every time, because it is the failure this deployment will
			// actually hit: the encoder is in the image, so the api boots, and the
			// device was never mapped into the container, so every job dies.
			return []Finding{okf(detail + fmt.Sprintf(", with %s visible on this host — check the api service maps it in (`devices: [\"/dev/dri:/dev/dri\"]`), because doctor reads the host's /dev and the transcodes read the container's",
				a.Device))}
		}
		return []Finding{okf(detail)}
	}
	// An unknown value: config refuses it at boot, so this is a report of a stack
	// that is not going to start, not a diagnosis of one that is.
	return []Finding{warnf(
		fmt.Sprintf("TRANSCODING_HW=%q is not a backend this version knows", configured),
		fmt.Sprintf("the accepted values are %s; the api refuses to boot on anything else, so fix it in %s",
			strings.Join(media.HardwareNames(), ", "), s.envRel))}
}

// hasNVIDIA reports whether this host looks like it has NVIDIA hardware: the
// control device the driver creates, or the tool that ships with it. Either is
// enough for a report; neither proves the container can see the card.
func (s *state) hasNVIDIA() bool {
	if _, err := s.opt.Host.Stat("/dev/nvidiactl"); err == nil {
		return true
	}
	if _, err := s.opt.Host.Stat("/dev/nvidia0"); err == nil {
		return true
	}
	_, err := s.opt.Host.LookPath("nvidia-smi")
	return err == nil
}

// ffmpegEncoders lists the encoder names the deployment's ffmpeg has, preferring
// the api container's binary over the host's. It returns the set, a phrase naming
// which binary answered, whether that binary was the CONTAINER's — which is the
// only one whose answer is a verdict — and, when neither could be asked, why not.
func (s *state) ffmpegEncoders(ctx context.Context) (list map[string]bool, where string, container bool, why string) {
	running, reason := s.containers(ctx)
	if reason == "" {
		if _, ok := serviceContainer(running, "api"); ok {
			args := s.composeArgs("exec", "-T", "api", "ffmpeg", "-hide_banner", "-encoders")
			out, err := s.opt.Host.Run(ctx, s.root, "docker", args...)
			if err == nil && out.ExitCode == 0 {
				return ffmpegEncoderNames(out.Stdout), "the api container's ffmpeg", true, ""
			}
		}
	}
	path, err := s.opt.Host.LookPath("ffmpeg")
	if err != nil {
		return nil, "", false, "the api container was not available and there is no ffmpeg on this host either"
	}
	out, err := s.opt.Host.Run(ctx, s.root, path, "-hide_banner", "-encoders")
	if err != nil || out.ExitCode != 0 {
		return nil, "", false, "the api container was not available and the host ffmpeg would not list its encoders"
	}
	return ffmpegEncoderNames(out.Stdout), "this host's ffmpeg (" + path + ")", false, ""
}

// ffmpegEncoderNames is the set of encoder names in `ffmpeg -encoders` output.
// The listing is a legend, a rule of dashes, and then one indented encoder per
// line as "<six flag characters> <name> <description>". The legend's own rows
// share that flag block ("V..... = Video"), so they are told apart by their
// second field being the "=" no encoder is named.
func ffmpegEncoderNames(out string) map[string]bool {
	names := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, " ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || len(fields[0]) != 6 || fields[1] == "=" {
			continue
		}
		names[fields[1]] = true
	}
	return names
}

// plural picks between two spellings for a count of 1 and a count of more.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
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
