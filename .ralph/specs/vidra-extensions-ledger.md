# Vidra Extensions Ledger

Vidra-specific features are tracked separately from PeerTube parity so Ralph does not confuse extras with completion of parity.

| id | feature | scope | backend requirements | frontend requirements | tests required | status | evidence | notes |
|---|---|---|---|---|---|---|---|---|
| VIDRA-ATPROTO | ATProto/Bluesky integration | federation | Modular config; enable with ActivityPub, alone, or disabled; SSRF-safe network; status/debug | Admin settings/status; remote identity indicators if used | integration, docs, federation smoke | NOT_STARTED | none | Must not break ActivityPub parity |
| VIDRA-IPFS-STORAGE | IPFS storage/distribution | storage/media | IPFS adapter/gateway/pinning, content metadata, fallback modes | IPFS status/config UI where supported | integration, smoke, Playwright | NOT_STARTED | none | Use uploaded Vidra IPFS UX docs when present |
| VIDRA-MSG-NORMAL | Normal secure messaging | messaging | server-side message body, attachments, SSRF-safe previews, disappearing timers | inbox/thread/composer/attachments/link previews/timer UI | integration, Playwright, security | NOT_STARTED | none | Separate from PeerTube parity |
| VIDRA-MSG-E2EE | Encrypted messaging | messaging/security | ciphertext-only storage, key/device metadata, test vectors, no homebrew crypto | encryption state UI, composer/thread, no plaintext logging | test vectors, integration, Playwright, security | NOT_STARTED | none | Block if threat model missing |
| VIDRA-WHISPER-CAPTIONS | Whisper auto captions | media/captions | optional worker/config, job status, caption review/edit | caption generation controls/status/edit flow | integration, smoke, Playwright | NOT_STARTED | none | Optional by config |
| VIDRA-CLAMAV | Virus scanning | security/media | ClamAV integration, fail-open/closed policy, scan states | upload status/error messaging | unit, integration, smoke | NOT_STARTED | none | Production should fail closed unless spec says otherwise |
| VIDRA-DONATION-WALLET | Simple verified crypto wallet display | profile/settings | address storage, challenge verification, no custody/no payment processing | profile wallet display, settings verification | unit, integration, Playwright | NOT_STARTED | none | Premium/payments remain deferred |
