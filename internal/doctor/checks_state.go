package doctor

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vidra/vidra-core/internal/dbmigrate"
	"github.com/vidra/vidra-core/internal/setup"
)

// searchLedgerTable is vidra-search's golang-migrate ledger. It is pinned here
// as a literal because the two repos share no module: vidra-search compiles the
// same name in (its own internal/dbmigrate.Table, "it MUST stay vidra_search_migrations
// … schema_migrations belongs to core"), and both sides read it through the same
// library, the same driver contract and the same postgres:// DSN. That shared
// shape is the only reason this check can exist from over here.
const searchLedgerTable = "vidra_search_migrations"

// checkSchemaLedger reads core's schema_migrations. It doubles as the database
// reachability check, which is why an unreachable database is a ✗ here and not a
// skip: nothing else in this report matters if the api cannot reach its data.
func checkSchemaLedger(ctx context.Context, s *state) []Finding {
	return []Finding{s.ledgerFinding(ctx, "core", "", "the api")}
}

// checkSearchLedger reads vidra-search's ledger over the same connection. The
// two services share one database (search lives in its own schema), so this is a
// second table read on a connection that is already proven, and a dirty search
// ledger blocks the search-migrate one-shot exactly the way a dirty core ledger
// blocks migrate.
func checkSearchLedger(ctx context.Context, s *state) []Finding {
	return []Finding{s.ledgerFinding(ctx, "search", searchLedgerTable, "the search service")}
}

func (s *state) ledgerFinding(ctx context.Context, label, table, consumer string) Finding {
	if s.envErr != nil {
		return skipf(fmt.Sprintf("the env file could not be read (%s), so there is no connection string", s.envErr))
	}
	dsn := s.value("DATABASE_URL")
	if dsn == "" {
		// The bundled Postgres. docker-compose.prod.yml resets its `ports:`, so it
		// is not reachable from the host AT ALL — deliberately. The ledger is
		// still readable, just from inside the network, which is what the api
		// image's own `migrate version` subcommand is for.
		return s.ledgerViaContainer(ctx, label, table)
	}
	st, err := s.opt.Prober.MigrationStatus(ctx, dsn, table)
	if err != nil {
		if label != "core" {
			// The core check has already reported the connection problem in full;
			// repeating it once per table is noise.
			return skipf("the database was not reachable for the core ledger either, so this table could not be read")
		}
		return failf(
			"the database is unreachable: "+reachSummary(err),
			"check DATABASE_URL in "+s.envRel+" and that this host can reach the managed instance (the provider's trusted-sources list is the usual culprit). Nothing the api does works until this does")
	}
	return ledgerStatusFinding(label, table, consumer, st, s.envRel)
}

// ledgerViaContainer reads the ledger from inside the stack, which is the only
// way when the bundled Postgres is used.
func (s *state) ledgerViaContainer(ctx context.Context, label, table string) Finding {
	if table != "" {
		// `api migrate version` reads core's ledger and takes no table argument.
		// Reaching the search table would mean a psql inside a container that may
		// not have one, for a fact the search-migrate one-shot already asserts on
		// every deploy.
		return skipf("this deployment uses the bundled Postgres, which publishes no host port, and the api image's `migrate version` only reports core's ledger")
	}
	running, why := s.containers(ctx)
	if why != "" {
		return skipf("this deployment uses the bundled Postgres (no host port, by design) and the running containers could not be inspected (" + why + ")")
	}
	if _, ok := serviceContainer(running, "api"); !ok {
		return skipf("this deployment uses the bundled Postgres, which publishes no host port by design, and the api container is not running to read the ledger from inside the network")
	}
	args := s.composeArgs("exec", "-T", "api", "migrate", "version")
	out, err := s.opt.Host.Run(ctx, s.root, "docker", args...)
	if err != nil {
		return skipf("the ledger could not be read from inside the api container (docker is not on this host's PATH)")
	}
	text := strings.TrimSpace(out.Stdout)
	if out.ExitCode != 0 && !strings.Contains(text, "version=") {
		return failf(
			"the database is unreachable from the api container: "+firstLine(orDefault(out.Stderr, text)),
			"check the bundled Postgres is up and healthy (`"+s.composeCommand("ps")+"`). Nothing the api does works until this does")
	}
	st, ok := parseMigrateVersion(text)
	if !ok {
		return skipf("the api container answered with something `migrate version` does not print: " + firstLine(text))
	}
	return ledgerStatusFinding(label, "", "the api", st, s.envRel)
}

func ledgerStatusFinding(label, table, consumer string, st dbmigrate.Status, envRel string) Finding {
	name := dbmigrate.Table
	if table != "" {
		name = table
	}
	switch {
	case st.Dirty:
		return failf(
			fmt.Sprintf("the %s migration ledger (%s) is DIRTY at version %d: a migration failed halfway and the schema state is unknown", label, name, st.Version),
			"do not deploy over it. Take a dump, work out from the migration's SQL whether it applied, then stamp the ledger to the truth with `docker compose … run --rm migrate migrate force <version> --yes-i-know` — that rewrites the ledger and runs no SQL, so the version you give it has to be the one the schema really is at")
	case !st.Applied:
		return warnf(
			fmt.Sprintf("no migration has ever run against this database (%s is empty), so %s has no schema to read", name, consumer),
			"deploy once: the migration one-shot runs before the api and brings the schema up. If this is a restored database, restore it before deploying rather than migrating an empty one")
	default:
		return okf(fmt.Sprintf("the %s migration ledger (%s) is at version %d and clean", label, name, st.Version))
	}
}

// parseMigrateVersion reads the api image's `migrate version` line, which is
// exactly `version=<n> dirty=<bool>` or `version=none dirty=false`.
func parseMigrateVersion(out string) (dbmigrate.Status, bool) {
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "version=") {
			line = strings.TrimSpace(l)
		}
	}
	if line == "" {
		return dbmigrate.Status{}, false
	}
	var st dbmigrate.Status
	for _, field := range strings.Fields(line) {
		k, v, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch k {
		case "version":
			if v == "none" {
				continue
			}
			var n uint
			if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
				return dbmigrate.Status{}, false
			}
			st.Version, st.Applied = n, true
		case "dirty":
			st.Dirty = v == "true"
		}
	}
	return st, true
}

// checkMediaGCPosture reads the two knobs that decide whether an unattended
// daily job is allowed to DELETE media, and says out loud what they currently
// mean. It reads the env file and nothing else — the api is the only thing that
// can say what the sweep actually did, and it says so in its own log and audit
// trail.
//
// It exists mostly to make the effective mode visible: "media garbage collection
// deletes orphaned objects every 24 hours" is a sentence most operators have
// never read, and the first time they want to know it is usually after something
// has gone missing. The two failure modes it can catch on its own are a
// misspelled boolean and an out-of-range percentage — both of which the api
// refuses to boot with, so a red line here is a deploy that will not come up.
func checkMediaGCPosture(_ context.Context, s *state) []Finding {
	if s.envErr != nil {
		return []Finding{skipf(fmt.Sprintf("the env file could not be read (%s)", s.envErr))}
	}
	const (
		enabledKey = "MEDIA_GC_ENABLED"
		percentKey = "MEDIA_GC_MAX_ORPHAN_PERCENT"
	)
	// The api parses both with the Go standard library, so doctor must too: an
	// env file that reads as configured to a human and does not parse for
	// strconv is exactly the drift worth catching before the deploy.
	enabled := true
	if raw := strings.TrimSpace(s.value(enabledKey)); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return []Finding{failf(
				fmt.Sprintf("%s is %q, which is not a boolean the api will accept (true|false|1|0)", enabledKey, truncate(raw, 20)),
				"set "+enabledKey+"=true or =false in "+s.envRel+". The api refuses to boot on an unparsable value, so this is a deploy that will not start rather than a sweep that misbehaves")}
		}
		enabled = v
	}
	percent := 25
	if raw := strings.TrimSpace(s.value(percentKey)); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return []Finding{failf(
				fmt.Sprintf("%s is %q, which is not a whole number", percentKey, truncate(raw, 20)),
				"set "+percentKey+" to a percentage between 0 and 100 in "+s.envRel+" (25 is the default). The api refuses to boot on an unparsable value")}
		}
		percent = v
	}
	if percent < 0 || percent > 100 {
		return []Finding{failf(
			fmt.Sprintf("%s is %d, which is not a percentage", percentKey, percent),
			"set "+percentKey+" between 0 and 100 in "+s.envRel+" (0 = never delete, 100 = no ratio limit, 25 = the default). The api refuses to boot on an out-of-range value")}
	}
	if !enabled {
		return []Finding{okf(fmt.Sprintf("%s=false — nothing sweeps on a schedule, so orphaned objects (a deleted video's HLS tree, a superseded original) accumulate and keep billing until an admin runs POST /api/v1/admin/media/gc by hand", enabledKey))}
	}
	switch percent {
	case 0:
		return []Finding{warnf(
			fmt.Sprintf("media garbage collection is on, but %s=0 means every destructive sweep of more than 100 orphans stops itself — in practice the sweep reports and never deletes", percentKey),
			"that is a safe place to sit while you watch what it would delete (the api logs `media gc sweep completed` with the counts each day). Raise it to 25 in "+s.envRel+" once the numbers look right")}
	case 100:
		return []Finding{warnf(
			fmt.Sprintf("media garbage collection is on with %s=100, which turns the circuit breaker off: a sweep will delete every unreferenced object it finds, however many that is", percentKey),
			"the breaker is what stops a sweep whose reference set came back wrong — a half-restored database looks exactly like 'almost everything is an orphan'. Set "+percentKey+"=25 in "+s.envRel+" unless you have a specific reason not to")}
	default:
		return []Finding{okf(fmt.Sprintf("media garbage collection runs daily and deletes stored objects no database row references; a sweep that finds more than %d%% of what it scanned to be orphans deletes nothing instead (%s). The first sweep after each restart is always a dry run", percent, percentKey))}
	}
}

// checkStorageMigration reports whether media is currently MOVING between
// storage backends, because a campaign in flight changes what three other
// things mean and an operator who does not know one is running will read all
// three wrong.
//
// While a campaign is live:
//
//   - media garbage collection is forced to a dry run, so the "GC deletes
//     orphans daily" posture above is temporarily untrue;
//   - presigned direct delivery is withheld entirely (the presigner is not
//     installed), so every byte proxies through the api and the delivery
//     configuration looks broken;
//   - a restore or an unplanned STORAGE_* edit lands in the middle of a move,
//     where "which store holds this object" is a question with two answers.
//
// It is a warning rather than a failure: a migration is a thing an operator
// deliberately started, and a ✗ for work proceeding as designed is how a report
// teaches people to ignore it.
func checkStorageMigration(ctx context.Context, s *state) []Finding {
	if s.envErr != nil {
		return []Finding{skipf(fmt.Sprintf("the env file could not be read (%s), so there is no connection string", s.envErr))}
	}
	dsn := s.value("DATABASE_URL")
	if dsn == "" {
		// The bundled Postgres publishes no host port by design. checkSchemaLedger
		// can still get its answer because the api image has a `migrate version`
		// subcommand that prints the ledger; there is no equivalent one-shot that
		// prints campaign state, and inventing one just for this would put a
		// diagnostic's convenience ahead of the api's surface area. The admin
		// storage-migration view answers it from inside.
		return []Finding{skipf("this deployment uses the bundled Postgres, which publishes no host port (by design), and there is no `migrate version`-style one-shot that reports storage-migration state — read it at GET /api/v1/admin/storage/migrations instead")}
	}
	active, err := s.opt.Prober.ActiveStorageMigration(ctx, dsn)
	if err != nil {
		if isMissingRelation(err) {
			// A database older than migration 0107. Nothing can be migrating,
			// because the feature does not exist there yet.
			return []Finding{okf("no storage migration in flight (this database predates the storage-migration tables, so there is nothing that could be)")}
		}
		return []Finding{skipf("the storage-migration state could not be read: " + reachSummary(err) + " (the core ledger check above reports the connection in full)")}
	}
	if !active {
		return []Finding{okf("no storage migration in flight — media is served from, and garbage-collected in, one store")}
	}
	return []Finding{warnf(
		"a storage migration campaign is IN FLIGHT: media is being copied to the STORAGE_MIGRATION_TARGET_* store. While it runs, media garbage collection is forced to a dry run (nothing is deleted), presigned direct delivery is withheld so every byte proxies through the api, and the two stores are deliberately out of step",
		"that is the designed behaviour, not a fault — let it finish. Do NOT run ./deploy/restore.sh and do NOT edit STORAGE_* except at the point the cutover runbook says to (\"Moving the media store\" in vidra-core/docs/operations.md): a restore mid-campaign reinstates rows for objects the move has not copied, and an early env swap makes the api serve from a store that is not finished. Watch progress at GET /api/v1/admin/storage/migrations; `docker compose … run --rm api verify-blobs` after the campaign completes is what proves the destination is whole")}
}

// isMissingRelation reports whether an error is Postgres saying the table is not
// there (SQLSTATE 42P01). It is matched on the message rather than on a typed
// error because the connection may fail before pgx can classify anything, and
// the only thing this distinction changes is a ✓ versus a ⚠.
func isMissingRelation(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "42p01") ||
		(strings.Contains(msg, "does not exist") && strings.Contains(msg, "relation"))
}

// maxBackupAge is how old the newest successful backup may be before it is a
// failure rather than a warning. The timer runs daily at 03:15 with up to 15
// minutes of jitter, so 26 hours is "one run has been missed", not "the schedule
// drifted".
const maxBackupAge = 26 * time.Hour

// checkBackupAge reads the marker deploy/backup.sh writes on success. The file
// is the only evidence that matters: a timer can be enabled, active and failing
// every night, and the unit's own status says nothing about whether a dump came
// out the other end.
func checkBackupAge(_ context.Context, s *state) []Finding {
	if skip, why := s.externalPostgresSkip(); skip {
		return []Finding{skipf(why)}
	}
	const rel = "backups/last_success"
	path := s.path("backups", "last_success")
	b, err := s.opt.Host.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Finding{warnf(
				"backups never succeeded here (or are not configured): "+rel+" does not exist",
				"run `./deploy/backup.sh` once by hand to prove it works, then install the timer (deploy/vidra-backup.service and .timer). A droplet with no dump is one `docker compose down -v` from being the whole company's outage")}
		}
		return []Finding{skipf(rel + " could not be read")}
	}
	stamp := strings.TrimSpace(strings.SplitN(strings.TrimSpace(string(b)), " ", 2)[0])
	when, perr := time.Parse(time.RFC3339, stamp)
	if perr != nil {
		// Fall back to the file's own mtime: the marker is REWRITTEN on every
		// success, so its timestamp carries the same fact.
		info, serr := s.opt.Host.Stat(path)
		if serr != nil {
			return []Finding{skipf(rel + " holds no timestamp this check could read")}
		}
		when = info.ModTime()
	}
	age := s.opt.Host.Now().Sub(when)
	switch {
	case age > maxBackupAge:
		return []Finding{failf(
			fmt.Sprintf("the last successful backup was %s ago (%s), which is more than the %s a daily schedule allows", roundAge(age), when.UTC().Format(time.RFC3339), maxBackupAge),
			"run `./deploy/backup.sh` now and read why it stopped: `journalctl -u vidra-backup.service -n 50`. Every hour this stays red is an hour of uploads that cannot be restored")}
	case age < 0:
		return []Finding{warnf(
			fmt.Sprintf("%s is dated in the future (%s), so the backup age could not be judged", rel, when.UTC().Format(time.RFC3339)),
			"check this host's clock (`timedatectl`): a skewed clock also mis-stamps every dump filename and every certificate check")}
	default:
		return []Finding{okf(fmt.Sprintf("the last successful backup was %s ago (%s)", roundAge(age), when.UTC().Format(time.RFC3339)))}
	}
}

// checkBackupTimer asks systemd whether the schedule exists at all. It is a
// separate check from the age above because the two fail independently and the
// fixes are different: an old dump is a broken backup, a missing timer is a
// backup nobody ever scheduled.
func checkBackupTimer(ctx context.Context, s *state) []Finding {
	if skip, why := s.externalPostgresSkip(); skip {
		return []Finding{skipf(why)}
	}
	const unit = "vidra-backup.timer"
	if _, err := s.opt.Host.LookPath("systemctl"); err != nil {
		return []Finding{skipf("this host has no systemctl, so there is no timer to ask about (a macOS or container dev box)")}
	}
	states := map[string]string{}
	for _, q := range []string{"is-enabled", "is-active"} {
		out, err := s.opt.Host.Run(ctx, s.root, "systemctl", q, unit)
		if err != nil {
			return []Finding{skipf("systemctl could not be run to check " + unit)}
		}
		// systemctl answers on stdout and exits non-zero for "disabled"/"inactive",
		// so the exit code carries no information the word does not.
		states[q] = firstLine(orDefault(out.Stdout, out.Stderr))
	}
	enabled, active := states["is-enabled"], states["is-active"]
	if enabled == "enabled" && active == "active" {
		return []Finding{okf(fmt.Sprintf("%s is enabled and active (daily at 03:15 with jitter)", unit))}
	}
	return []Finding{failf(
		fmt.Sprintf("%s is %s and %s, so no backup is scheduled on this host", unit, enabled, active),
		"install and start it: `cp deploy/vidra-backup.{service,timer} /etc/systemd/system/ && systemctl daemon-reload && systemctl enable --now "+unit+"`. Check it took with `systemctl list-timers "+unit+"`")}
}

// externalPostgresSkip is why the backup checks stand down on a managed
// database: deploy/backup.sh and restore.sh exec pg_dump/pg_restore INSIDE the
// bundled container and refuse to run without one, so there is no marker to be
// old and no timer to be missing. Reporting a permanent ✗ for a deployment whose
// backups are the provider's would be teaching operators to ignore this section.
func (s *state) externalPostgresSkip() (bool, string) {
	if !setup.IsTrue(s.value("VIDRA_EXTERNAL_POSTGRES")) {
		return false, ""
	}
	return true, "VIDRA_EXTERNAL_POSTGRES=true — deploy/backup.sh refuses to run against a managed database (it execs pg_dump inside the bundled container), so backups here are the provider's automated ones. Confirm they are on, and that point-in-time restore is enabled"
}

const (
	gib = 1 << 30
	// The tiers. Below the warn line a deployment still works and an operator has
	// time; below the fail line a transcode's scratch write, a Postgres WAL
	// segment or a certificate renewal is about to fail instead.
	warnFreeFraction = 0.10
	warnFreeBytes    = 5 * gib
	failFreeFraction = 0.05
	failFreeBytes    = 2 * gib
)

// checkDiskSpace measures the filesystems this deployment writes to: the
// checkout (backups land in backups/ beside it) and the docker data root, which
// is where every named volume lives — including transcode_tmp, the api's TMPDIR
// and the one that fills fastest, because a single HLS ladder writes several
// times the source file before anything is uploaded.
func checkDiskSpace(ctx context.Context, s *state) []Finding {
	type target struct{ label, path string }
	targets := []target{{"the checkout (and backups/)", s.root}}

	info, why := s.dockerInfo(ctx)
	if why == "" && strings.TrimSpace(info.DockerRootDir) != "" {
		targets = append(targets, target{"docker's data root " + info.DockerRootDir + " (every named volume, incl. the transcode scratch)", info.DockerRootDir})
	}

	var findings []Finding
	seen := map[DiskUsage]bool{}
	for _, t := range targets {
		usage, err := s.opt.Host.DiskUsage(t.path)
		if err != nil {
			findings = append(findings, skipf("free space on "+t.path+" could not be measured"))
			continue
		}
		if seen[usage] {
			// The same filesystem under two names — the ordinary single-disk
			// droplet. One line is the whole truth.
			continue
		}
		seen[usage] = true
		findings = append(findings, diskFinding(t.label, usage))
	}
	if why != "" {
		findings = append(findings, skipf("docker's data root could not be located ("+why+"), so the volume filesystem — where the transcode scratch lives — was not measured"))
	}
	if len(findings) == 0 {
		return []Finding{skipf("no filesystem could be measured")}
	}
	return findings
}

func diskFinding(label string, u DiskUsage) Finding {
	pct := u.FreeFraction() * 100
	detail := fmt.Sprintf("%s: %s free of %s (%.0f%%)", label, humanBytes(u.FreeBytes), humanBytes(u.TotalBytes), pct)
	const fix = "free space or grow the disk before the next upload: a transcode writes several times the source file into the scratch volume, and Postgres stops accepting writes when its filesystem fills. `docker system prune -f` and trimming backups/ are the two quickest wins"
	switch {
	case u.FreeFraction() < failFreeFraction || u.FreeBytes < failFreeBytes:
		return failf(detail, fix)
	case u.FreeFraction() < warnFreeFraction || u.FreeBytes < warnFreeBytes:
		return warnf(detail, fix)
	default:
		return okf(detail)
	}
}

func humanBytes(b uint64) string {
	switch {
	case b >= gib:
		return fmt.Sprintf("%.1f GiB", float64(b)/gib)
	case b >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(b)/(1<<20))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func roundAge(d time.Duration) string {
	if d >= time.Hour {
		return d.Round(time.Minute).String()
	}
	return d.Round(time.Second).String()
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

// reachSummary turns a connection failure into the one clause an operator can
// act on. The rest of a pgx/lib/pq error is the DSN it tried, which may carry a
// password.
func reachSummary(err error) string {
	msg := firstLine(err.Error())
	for _, marker := range []string{"timed out", "connection refused", "no such host", "password authentication failed", "does not exist", "SSL", "certificate"} {
		if strings.Contains(strings.ToLower(msg), strings.ToLower(marker)) {
			return marker
		}
	}
	return "the connection did not complete"
}
