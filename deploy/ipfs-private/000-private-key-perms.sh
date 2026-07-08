#!/bin/sh
# Root entrypoint PRELUDE for the private-swarm kubo nodes (fix_plan P19.P, the
# ipfs-private-integration CI-never-green fix).
#
# WHY THIS EXISTS: the ipfs/kubo entrypoint (/usr/local/bin/start_ipfs) drops from root
# to the unprivileged `ipfs` user (uid 1000, gid 100) via gosu BEFORE it runs the
# /container-init.d hooks. Our dev-key hook (001-init-private-swarm.sh) must WRITE the
# shared swarm.key + peer-id files into the `ipfs_private_key` named volume mounted at
# /private-key — but a fresh named volume is created ROOT-owned, so the ipfs user's
# write fails with "Permission denied", `set -eu` aborts the hook, and the daemon exits
# 123 (the dependent kubo-private-2 / ipfs-cluster-private services then never start).
# That is exactly why the auto-gen dev path — and thus the whole compose bring-up — was
# red on every commit.
#
# This prelude runs as ROOT (it IS the container entrypoint, before kubo's own
# root->ipfs gosu handoff), makes /private-key writable by the ipfs user, then execs the
# stock kubo entrypoint UNCHANGED so all of kubo's normal init still happens.
#
# Non-recursive + best-effort by design so BOTH paths work:
#   * dev auto-gen: chowns the empty root-owned volume dir so the hook can create the key.
#   * operator-mounted read-only key (production): the key file is mounted read-only and
#     already exists, so nothing is written to /private-key; the chown either succeeds
#     harmlessly on the dir or fails (read-only mount) and is swallowed by `|| true`.
#     We never chown the key file itself (non-recursive), so its read-only bit is kept.
set -eu

if [ "$(id -u)" -eq 0 ] && [ -d /private-key ]; then
	chown ipfs:users /private-key 2>/dev/null || true
fi

exec /sbin/tini -- /usr/local/bin/start_ipfs "$@"
