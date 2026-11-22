package main

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func TestSetupATProtoProviderAndKeySurviveReruns(t *testing.T) {
	h := newHarness(t)
	if err := h.run(h.setupArgs("--atproto")...); err != nil {
		t.Fatal(err)
	}
	file := h.readOutput(t)
	if !strings.Contains(h.out.String(), "deploy/atproto-pds.py --prepare") || !strings.Contains(h.out.String(), "./deploy/compose.sh config -q") {
		t.Fatal("local validation must prepare the PDS and render its deployment overlay")
	}
	key := valueOf(t, file, "ATPROTO_KEY_KEK")
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) != 32 || valueOf(t, file, "ATPROTO_ENABLED") != "true" || valueOf(t, file, "ATPROTO_PDS_URL") != "" {
		t.Fatal("fresh ATProto install needs a stable encryption key and the local Docker default")
	}
	for _, tc := range []struct {
		args         []string
		on, provider string
	}{
		{[]string{"--atproto-pds", "https://pds.example.org/"}, "true", "https://pds.example.org"},
		{[]string{"--atproto"}, "true", "https://pds.example.org"},
		{nil, "true", "https://pds.example.org"},
		{[]string{"--atproto=false"}, "false", "https://pds.example.org"},
		{[]string{"--atproto-pds", "local"}, "true", ""},
	} {
		if err := h.run(h.setupArgs(append([]string{"--yes"}, tc.args...)...)...); err != nil {
			t.Fatal(err)
		}
		file = h.readOutput(t)
		if valueOf(t, file, "ATPROTO_ENABLED") != tc.on || valueOf(t, file, "ATPROTO_PDS_URL") != tc.provider || valueOf(t, file, "ATPROTO_KEY_KEK") != key {
			t.Fatal("setup did not preserve the selected provider and credential-encryption key")
		}
		if strings.Contains(h.out.String()+h.err.String(), key) {
			t.Fatal("setup printed the encryption key")
		}
	}
}

func TestSetupATProtoUsesExistingFederationKey(t *testing.T) {
	h := newHarness(t)
	if err := h.run(h.setupArgs("--federation", "--atproto-pds", "https://pds.example.org")...); err != nil {
		t.Fatal(err)
	}
	file := h.readOutput(t)
	if valueOf(t, file, "FEDERATION_KEY_KEK") == "" || strings.Contains(file, "\nATPROTO_KEY_KEK=") {
		t.Fatal("setup must preserve the configured federation-key fallback")
	}
	if strings.Contains(h.out.String(), "deploy/atproto-pds.py --prepare") {
		t.Fatal("external PDS setup requested a local container")
	}
}

func TestSetupATProtoRejectsInvalidSelection(t *testing.T) {
	for _, args := range [][]string{
		{"--atproto-pds", "http://pds.example.org"},
		{"--atproto-pds", "https://pds.example.org/xrpc"},
		{"--atproto=false", "--atproto-pds", "local"},
		{"--atproto", "--tls-mode", "plain-http"},
		{"--atproto", "--domain", "example.123"},
		{"--atproto", "--domain", strings.Repeat("a", 64) + ".org"},
	} {
		h := newHarness(t)
		if err := h.run(h.setupArgs(args...)...); err == nil {
			t.Fatal("accepted invalid PDS selection")
		}
		if _, err := os.Stat(h.output); !os.IsNotExist(err) {
			t.Fatal("invalid choice wrote the environment")
		}
	}
}

func TestSetupATProtoExistingInstallationNeedsOriginalKey(t *testing.T) {
	h := newHarness(t)
	if err := h.run(h.setupArgs()...); err != nil {
		t.Fatal(err)
	}
	before := h.readOutput(t)
	if err := h.run(h.setupArgs("--yes", "--atproto")...); err == nil || !strings.Contains(err.Error(), "ATPROTO_KEY_KEK") {
		t.Fatal("setup must explain how to restore the missing key")
	}
	if h.readOutput(t) != before {
		t.Fatal("changed an installation lacking its key")
	}
}
