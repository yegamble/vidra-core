// Package ipfsmirror is the hybrid IPFS media-mirroring sidecar (fix_plan P19,
// .ralph/specs/ipfs-media.md). IPFS is NOT an authoritative backend: local/S3
// keeps the only authoritative copy. When IPFS_ENABLED, this package observes
// authoritative writes, and for objects that pass the PUBLIC-only eligibility
// gate it enqueues an add+pin intent into the media_ipfs_pins ledger; a worker
// then streams the bytes to a Kubo node and records the CID. Nothing
// private/unlisted/quarantined/DM/export/transient/remote is ever enqueued — the
// eligibility gate (eligibility.go) is the privacy fence and is default-deny.
package ipfsmirror

// MediaClass identifies a kind of stored media object. The first block is the
// set the mirror may pin (already-public content); the second block is the
// never-mirror set, kept as named constants ONLY so the eligibility table can
// assert, explicitly and permanently, that they are never enqueued. Persisted in
// media_ipfs_pins.media_class for the mirror-eligible classes.
type MediaClass string

// Mirror-eligible classes (still subject to the per-object eligibility gate).
const (
	// ClassVideoOriginal is web-videos/<id><ext> (video_files kind='original').
	ClassVideoOriginal MediaClass = "video_original"
	// ClassHLS is the finalized VOD HLS tree streaming-playlists/<id>/ (one
	// directory/wrap add → one car_root CID). Wired in P19.4; the class exists
	// here so the eligibility gate and admin counts already understand it.
	ClassHLS MediaClass = "hls"
	// ClassWebM is the progressive VP9/WebM alternate (video_files kind='webm').
	ClassWebM MediaClass = "webm"
	// ClassThumbnail is thumbnails/<id>.jpg (video_files kind='thumbnail').
	ClassThumbnail MediaClass = "thumbnail"
	// ClassStoryboard is the storyboard sprite sheet (video_files kind='storyboard').
	ClassStoryboard MediaClass = "storyboard"
	// ClassStoryboardVTT is the storyboard WebVTT map (video_files kind='storyboard_vtt').
	ClassStoryboardVTT MediaClass = "storyboard_vtt"
	// ClassCaption is a per-language subtitle track (captions.storage_key).
	ClassCaption MediaClass = "caption"
	// ClassUserAvatar / ClassUserBanner are a user's identity images.
	ClassUserAvatar MediaClass = "user_avatar"
	ClassUserBanner MediaClass = "user_banner"
	// ClassChannelAvatar / ClassChannelBanner are a channel's identity images.
	ClassChannelAvatar MediaClass = "channel_avatar"
	ClassChannelBanner MediaClass = "channel_banner"
	// ClassPlaylistCover is a playlist cover image (playlists.thumbnail_ext).
	ClassPlaylistCover MediaClass = "playlist_cover"
)

// Never-mirror classes. These are NEVER added to a public IPFS network in v1
// (privacy). They are declared so the eligibility table can assert-not-enqueue
// for each, turning "we forgot to handle X" into a failing test. The gate
// (eligibility.go) is default-deny, so an unlisted class is also refused — these
// constants make the intent explicit and regression-proof.
const (
	// ClassDMAttachment is a private direct-message attachment (E2EE / participant
	// -gated). Never public; if ever replicated it must be via a private cluster
	// and coordinated with the Messaging v2 spec (spec §7).
	ClassDMAttachment MediaClass = "dm_attachment"
	// ClassAccountExport is a private, transient, PII account-export archive.
	ClassAccountExport MediaClass = "account_export"
	// ClassUploadChunk is an in-flight resumable-upload chunk (assembled then dropped).
	ClassUploadChunk MediaClass = "upload_chunk"
	// ClassLiveEdge is the mutable live HLS edge (only the finalized replay VOD is
	// ever eligible, and only after it passes the public gate).
	ClassLiveEdge MediaClass = "live_edge"
	// ClassRemoteThumbnail is a cached federated-video thumbnail — not our content
	// to redistribute.
	ClassRemoteThumbnail MediaClass = "remote_thumbnail"
)

// videoDerivedClasses are the mirror-eligible classes whose eligibility is
// governed by the parent video's privacy+state (spec §3 rows 1-7).
func isVideoDerived(c MediaClass) bool {
	switch c {
	case ClassVideoOriginal, ClassHLS, ClassWebM, ClassThumbnail,
		ClassStoryboard, ClassStoryboardVTT, ClassCaption:
		return true
	}
	return false
}

// isIdentityImage reports whether c is a user/channel avatar or banner (spec §3
// rows 8-9), gated by the owning account's unlisted/active flags.
func isIdentityImage(c MediaClass) bool {
	switch c {
	case ClassUserAvatar, ClassUserBanner, ClassChannelAvatar, ClassChannelBanner:
		return true
	}
	return false
}
