package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "atproto":
		keep := false
		for _, arg := range os.Args[2:] {
			if arg == "--keep" {
				keep = true
			}
		}
		os.Exit(runATProto(keep))

	case "activitypub":
		baseURL := os.Getenv("VIDRA_BASE_URL")
		if baseURL == "" {
			baseURL = "http://localhost:8080"
		}
		username := os.Getenv("VIDRA_USERNAME")
		if username == "" {
			username = "admin"
		}
		testRemote := false
		for _, arg := range os.Args[2:] {
			if arg == "--test-remote" {
				testRemote = true
			}
		}
		os.Exit(runActivityPub(baseURL, username, testRemote))

	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage: verify-federation <subcommand> [flags]

Subcommands:
  atproto       Verify ATProto (BlueSky) federation
  activitypub   Verify ActivityPub endpoint compliance

ATProto flags:
  --keep        Don't delete the test post after verification

ATProto env vars (required):
  ATPROTO_PDS_URL        PDS URL (e.g., https://bsky.social)
  ATPROTO_HANDLE         Your handle (e.g., yourname.bsky.social)
  ATPROTO_APP_PASSWORD   App-specific password

ActivityPub flags:
  --test-remote   Also fetch a remote Mastodon actor

ActivityPub env vars (optional):
  VIDRA_BASE_URL    Base URL of running Vidra instance (default: http://localhost:8080)
  VIDRA_USERNAME    Username to verify (default: admin)
`)
}
