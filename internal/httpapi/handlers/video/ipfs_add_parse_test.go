package video_test

import (
	"bytes"
	"testing"
)

func TestParseIPFSAddResponse_SingleLine(t *testing.T) {
	ndjson := `{"Name":"avatar.png","Hash":"bafybeigdyrzt3","Size":"1234"}`
	cid, err := parseIPFSAddResponse(bytes.NewBufferString(ndjson))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cid != "bafybeigdyrzt3" {
		t.Fatalf("unexpected cid: %s", cid)
	}
}

func TestParseIPFSAddResponse_MultiLine_LastWins(t *testing.T) {
	ndjson := "" +
		`{"Name":"avatar.tmp","Hash":"bafyAAA","Size":"100"}` + "\n" +
		`{"Name":"avatar.png","Hash":"bafyBBB","Size":"1234"}` + "\n"
	cid, err := parseIPFSAddResponse(bytes.NewBufferString(ndjson))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cid != "bafyBBB" {
		t.Fatalf("expected final cid bafyBBB, got %s", cid)
	}
}

func TestParseIPFSAddResponse_Empty(t *testing.T) {
	_, err := parseIPFSAddResponse(bytes.NewBuffer(nil))
	if err == nil {
		t.Fatalf("expected error for empty response")
	}
}
