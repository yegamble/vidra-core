#!/usr/bin/env bash
# Audit the PREVIOUS RELEASE's integration log against the register of compat
# breaks the team has accepted in writing.
#
# WHY THIS EXISTS. schema-compat.yml proves the one-release policy by running
# release N-1's own suite against release N's schema, and until now that lane
# had exactly two states: green, or a wall the change had to be abandoned at. A
# TIGHTENING — unifying two namespaces, say — cannot be green by construction,
# and the two alternatives available without this script were both bad: edit the
# lane to stop asserting (a silent hole), or stage the constraint over two
# releases (a real hole, in the product, for a release).
#
# So this is the third option, and it is the one rollback-floor.yml already uses
# for its own known break: name the consequence, assert it, and make it
# self-clearing. The lane consults the register ONLY when a capability probe
# says N-1 predates the migration that caused the break. The moment N-1 carries
# it, the caller stops passing a register at all and the whole suite must pass.
#
# THE ASSERTION IS TWO-SIDED, which is what keeps it from becoming a mute:
#
#   * an UNREGISTERED failure fails the job — the whole point of the lane;
#   * a REGISTERED test that PASSED fails the job as a stale expectation, so an
#     entry cannot outlive the break it describes;
#   * a log with no passing test at all fails the job, on the same doctrine as
#     the skip audit: a lane that ran nothing must not look like one that proved
#     something.
#
# Usage: assert-prev-release-failures.sh <go-test-log> <expectations-file>
set -euo pipefail

log=${1:?usage: assert-prev-release-failures.sh <log> <expectations>}
expect=${2:?usage: assert-prev-release-failures.sh <log> <expectations>}

[ -r "$log" ] || { echo "::error::prev-release audit: cannot read test log $log" >&2; exit 1; }
[ -r "$expect" ] || { echo "::error::prev-release audit: cannot read register $expect" >&2; exit 1; }

# Top-level failures only: `go test` indents subtest results, and a subtest
# failure always carries its parent, so the parent name is the stable key.
failed=$(grep -E '^--- FAIL: ' "$log" | sed -E 's/^--- FAIL: ([^ ]+).*/\1/' | sort -u || true)
passed=$(grep -cE '^(ok|--- PASS: )' "$log" || true)

if [ "${passed:-0}" -eq 0 ]; then
  echo "::error::prev-release audit: $log records no passing package or test — the previous release's suite proved nothing here." >&2
  exit 1
fi

registered=$(grep -vE '^[[:space:]]*(#|$)' "$expect" | sort -u || true)

status=0
while IFS= read -r name; do
  [ -n "$name" ] || continue
  if printf '%s\n' "$registered" | grep -qxF -- "$name"; then
    echo "  accepted break: ${name} — registered in $(basename "$expect")"
  else
    status=1
    echo "::error::prev-release audit: ${name} FAILED against the new schema and is NOT a registered compat break. Read its output above: either the migration broke a read/write path the previous release depends on, or the break is real and must be accepted in writing in $(basename "$expect") before this can merge." >&2
  fi
done <<EOF
$failed
EOF

# A register entry that no longer fails has outlived its break — usually because
# the previous release now carries the migration, which is exactly when the
# caller should stop passing a register at all.
while IFS= read -r name; do
  [ -n "$name" ] || continue
  if ! printf '%s\n' "$failed" | grep -qxF -- "$name"; then
    status=1
    echo "::error::prev-release audit: ${name} is registered as an accepted compat break in $(basename "$expect") but PASSED. Remove the entry — a register that outlives its break silences the next real one." >&2
  fi
done <<EOF
$registered
EOF

if [ "$status" -ne 0 ]; then
  exit 1
fi

n=$(printf '%s\n' "$registered" | grep -c . || true)
echo "OK: prev-release audit — ${n} registered compat break(s) failed as expected, nothing else did."
