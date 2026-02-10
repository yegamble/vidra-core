# Athena Audit & Improvement Tracker

This checklist tracks implementation progress against the current audit and improvement plan.

## Architecture Documentation

- [x] Add architecture diagrams (component, sequence, ER) to docs.
- [x] Update `docs/architecture.md` to reflect implemented features and naming consistency.
- [x] Document additional subsystems: P2P/Torrent, live streaming, and chat.
- [ ] Verify all architecture text against current code paths for edge cases.
- [ ] Add a full system overview of all third-party integrations with operational ownership.

## Testing Strategy

- [ ] Re-run and baseline current coverage with `go test -coverprofile`.
- [ ] Ensure each recently added handler/service has explicit tests.
- [ ] Expand E2E flow coverage for upload/encode/playback and live lifecycle.
- [ ] Expand security tests per strategy document.
- [ ] Enforce coverage thresholds in CI.

## CI/CD & act Compatibility

- [x] Expand `.actrc` runner image mappings beyond `self-hosted`.
- [x] Add local `act` usage documentation with secret requirements.
- [ ] Verify every workflow/job under `act` and document known exceptions.
- [ ] Add/confirm workflow guards for publish/upload steps not needed locally.

## PeerTube Compatibility

- [ ] Re-validate channel/account model and endpoint usage of `channel_id`.
- [ ] Confirm API shapes for playlists, comments, and captions in OpenAPI.
- [ ] Add admin/oEmbed endpoint tests where still missing.
- [ ] Add migration guide from PeerTube DB/storage/config.
- [ ] Add dedicated OpenAPI compat tags/routes where needed.

## General Improvements

- [x] Add `CONTRIBUTING.md`.
- [x] Add `CODE_OF_CONDUCT.md`.
- [x] Add a top-level docs index at `docs/README.md`.
- [x] Remove stale patch reject artifacts (`Oops.rej`).
- [ ] Reconcile README features into implemented vs planned sections.
