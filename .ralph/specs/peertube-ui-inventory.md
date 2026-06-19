# PeerTube UI Inventory

This file tracks PeerTube-visible UI down to controls/buttons and maps it to Vidra implementation and tests.

Status values: `NOT_STARTED`, `SURVEYED`, `IMPLEMENTED`, `VERIFIED`, `INTENTIONAL_DIFFERENCE`, `DEFERRED`, `BLOCKED`.

## Inventory template

For every route/flow, use this structure:

```md
## PT-AREA-ROUTE-NAME — Human readable page name

- Reference route/page: TBD
- Vidra route/component: TBD
- Auth states: logged-out / logged-in / owner / moderator / admin / remote / blocked / banned / permission denied
- Responsive states: mobile / tablet / desktop
- Backend dependencies: TBD
- Playwright spec: TBD
- Status: NOT_STARTED

### Controls

| control id | label / accessible name | type | states | behavior | backend dependency | tests | status | evidence |
|---|---|---|---|---|---|---|---|---|
| PT-AREA-CONTROL | TBD | button/menu/tab/input/modal | default/hover/focus/disabled/loading/error | TBD | TBD | TBD | NOT_STARTED | none |
```

## Seed areas Ralph must expand

### PT-NAV — Shell/navigation

| control id | label / accessible name | type | states | behavior | backend dependency | tests | status | evidence |
|---|---|---|---|---|---|---|---|---|
| PT-NAV-PUBLISH | Publish | nav button/link | logged-in/disabled if no upload rights | Opens publish flow | auth, quota, instance upload config | Playwright nav | NOT_STARTED | none |
| PT-NAV-SEARCH | Search | input/button | empty/focused/results/error | Search local/global/URI/handle | search API | Playwright search | NOT_STARTED | none |
| PT-NAV-MY-LIBRARY | My library | nav link | logged-in only | Opens library/history/playlists | auth | Playwright auth nav | NOT_STARTED | none |
| PT-NAV-MY-VIDEO-SPACE | My video space | nav group | creator/admin | Shows videos/channels/studio | auth/permissions | Playwright | NOT_STARTED | none |
| PT-NAV-ADMINISTRATION | Administration | nav group | admin/mod only | Opens admin sections | RBAC | Playwright RBAC | NOT_STARTED | none |

### PT-WATCH — Watch page/player

| control id | label / accessible name | type | states | behavior | backend dependency | tests | status | evidence |
|---|---|---|---|---|---|---|---|---|
| PT-WATCH-PLAY-PAUSE | Play/Pause | player button | playing/paused/loading/error | Toggle playback | HLS/file URLs | Playwright player | NOT_STARTED | none |
| PT-WATCH-VOLUME | Volume/Mute | player control | muted/unmuted/focused | Adjust/mute audio | none | Playwright/a11y | NOT_STARTED | none |
| PT-WATCH-FULLSCREEN | Fullscreen | player button | supported/unsupported | Enter/exit fullscreen | none | Playwright | NOT_STARTED | none |
| PT-WATCH-QUALITY | Quality | player menu | multiple/single/disabled | Select video rendition | video files/renditions API | Playwright | NOT_STARTED | none |
| PT-WATCH-SPEED | Speed | player menu | default/selected | Select playback speed | none | Playwright | NOT_STARTED | none |
| PT-WATCH-CAPTIONS | Captions/Subtitles | player menu | captions/no captions | Select captions | captions API | Playwright | NOT_STARTED | none |
| PT-WATCH-LIKE | Like | action button | logged-out/logged-in/liked | Like/unlike | ratings API | integration/Playwright | NOT_STARTED | none |
| PT-WATCH-DISLIKE | Dislike | action button | logged-out/logged-in/disliked | Dislike/undo | ratings API | integration/Playwright | NOT_STARTED | none |
| PT-WATCH-SAVE | Save to playlist / Watch later | menu/button | logged-out/logged-in/saved | Opens playlist save menu | playlist API | Playwright | NOT_STARTED | none |
| PT-WATCH-SHARE | Share | button/modal | default/open/copied | Opens share/embed/download options | video URL/embed | Playwright | NOT_STARTED | none |
| PT-WATCH-DOWNLOAD | Download | button/modal | allowed/forbidden | Select downloadable resolution | files/download API | Playwright | NOT_STARTED | none |
| PT-WATCH-REPORT | Report | menu/modal | logged-out/logged-in | Opens report content flow | reports API | integration/Playwright | NOT_STARTED | none |
| PT-WATCH-SUPPORT | Support | button/link | present/absent | Shows creator support text/link | video support metadata | Playwright | NOT_STARTED | none |
| PT-WATCH-COMMENT-SUBMIT | Comment | composer button | disabled/loading/error | Adds comment | comments API/federation | integration/Playwright | NOT_STARTED | none |

### PT-PUBLISH — Publish/upload/live

| control id | label / accessible name | type | states | behavior | backend dependency | tests | status | evidence |
|---|---|---|---|---|---|---|---|---|
| PT-PUBLISH-TAB-FILE | Upload file | tab | selected/unselected | Shows file upload form | upload enabled | Playwright | NOT_STARTED | none |
| PT-PUBLISH-FILE-SELECT | Select file to upload | button/input | empty/uploading/error | Starts upload | upload API/storage/scan | integration/Playwright | NOT_STARTED | none |
| PT-PUBLISH-TAB-URL | Import with URL | tab | enabled/disabled | Shows URL importer | import config/SSRF | fuzz/integration/Playwright | NOT_STARTED | none |
| PT-PUBLISH-URL-IMPORT | Import | button | disabled/loading/error | Starts remote URL import | importer job | integration/Playwright | NOT_STARTED | none |
| PT-PUBLISH-TAB-TORRENT | Import with torrent | tab | enabled/disabled | Shows torrent/magnet importer | torrent importer | integration/Playwright | NOT_STARTED | none |
| PT-PUBLISH-GO-LIVE | Go live | tab/button | enabled/disabled | Creates live stream | RTMP/HLS config | smoke/Playwright | NOT_STARTED | none |
| PT-PUBLISH-PRIVACY | Privacy | select/radio | public/unlisted/private/internal | Sets visibility | permissions/federation | integration/Playwright | NOT_STARTED | none |
| PT-PUBLISH-CHANNEL | Channel | select | empty/multiple | Selects owning channel | channel API | integration/Playwright | NOT_STARTED | none |
| PT-PUBLISH-SAVE | Save/Publish | button | disabled/loading/success/error | Saves metadata/publishes | videos API/jobs | integration/Playwright | NOT_STARTED | none |

### PT-ADMIN — Administration/moderation

| control id | label / accessible name | type | states | behavior | backend dependency | tests | status | evidence |
|---|---|---|---|---|---|---|---|---|
| PT-ADMIN-USERS-CREATE | Create user | button/modal | admin only | Creates local user | users API/RBAC | integration/Playwright | NOT_STARTED | none |
| PT-ADMIN-USERS-EDIT | Edit user | row action | admin/mod perms | Opens user edit | users API/RBAC | integration/Playwright | NOT_STARTED | none |
| PT-ADMIN-USERS-BAN | Ban | destructive action | confirm/cancel | Bans account | users API/audit | integration/Playwright | NOT_STARTED | none |
| PT-ADMIN-REPORT-ACCEPT | Accept report | action | loading/success/error | Accepts report and notifies reporter | reports API/notifications | integration/Playwright | NOT_STARTED | none |
| PT-ADMIN-REPORT-REJECT | Reject report | action | loading/success/error | Rejects report and notifies reporter | reports API/notifications | integration/Playwright | NOT_STARTED | none |
| PT-ADMIN-VIDEO-BLOCK | Block video | action | local/remote | Blocks local/remote video | block API/federation | integration/Playwright | NOT_STARTED | none |
| PT-ADMIN-WATCHED-WORDS-SAVE | Save watched words list | button | validation/loading/error | Saves moderation watched words | watched words API | integration/Playwright | NOT_STARTED | none |

## Rule

Ralph must expand this inventory whenever it discovers a new page, action, menu item, admin workflow, player control, or responsive variant. Do not mark a route complete until every visible control has a status and evidence.
