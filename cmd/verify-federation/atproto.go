package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type atprotoResult struct {
	passed  int
	failed  int
	postURI string
	postCID string
}

func runATProto(keep bool) int {
	fmt.Println("=== ATProto Federation Verification ===")

	pdsURL := strings.TrimRight(os.Getenv("ATPROTO_PDS_URL"), "/")
	handle := os.Getenv("ATPROTO_HANDLE")
	appPassword := os.Getenv("ATPROTO_APP_PASSWORD")

	if pdsURL == "" || handle == "" || appPassword == "" {
		fmt.Println("[FAIL] Missing required env vars: ATPROTO_PDS_URL, ATPROTO_HANDLE, ATPROTO_APP_PASSWORD")
		return 1
	}

	client := &http.Client{Timeout: 15 * time.Second}
	result := &atprotoResult{}

	// Step 1: Authenticate
	accessJWT, did, ok := atprotoCreateSession(client, pdsURL, handle, appPassword, result)
	if !ok {
		printATProtoSummary(result)
		return 1
	}

	// Step 2: Publish test post
	ok = atprotoPublishPost(client, pdsURL, accessJWT, did, result)
	if !ok {
		printATProtoSummary(result)
		return 1
	}

	// Step 3: Fetch post back
	atprotoFetchPost(client, pdsURL, result)

	// Step 4: Check author feed
	atprotoCheckFeed(client, pdsURL, did, result)

	// Step 5: Output explorer URL
	rkey := extractRkey(result.postURI)
	if rkey != "" {
		fmt.Printf("[INFO] View on BlueSky: https://bsky.app/profile/%s/post/%s\n", handle, rkey)
	}

	// Step 6: Cleanup
	if !keep && result.postURI != "" {
		atprotoDeletePost(client, pdsURL, accessJWT, did, rkey, result)
	} else if keep {
		fmt.Println("[SKIP] Post retained (--keep flag)")
	}

	printATProtoSummary(result)
	if result.failed > 0 {
		return 1
	}
	return 0
}

func atprotoCreateSession(client *http.Client, pdsURL, handle, appPassword string, result *atprotoResult) (string, string, bool) {
	body, _ := json.Marshal(map[string]string{
		"identifier": handle,
		"password":   appPassword,
	})

	resp, err := doPost(client, pdsURL+"/xrpc/com.atproto.server.createSession", body, "")
	if err != nil {
		fmt.Printf("[FAIL] Session creation failed: %v\n", err)
		result.failed++
		return "", "", false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Printf("[FAIL] Session creation failed (HTTP %d): %s\n", resp.StatusCode, string(respBody))
		result.failed++
		return "", "", false
	}

	var session struct {
		AccessJWT string `json:"accessJwt"`
		DID       string `json:"did"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		fmt.Printf("[FAIL] Session response decode failed: %v\n", err)
		result.failed++
		return "", "", false
	}

	fmt.Printf("[PASS] Session created (DID: %s)\n", session.DID)
	result.passed++
	return session.AccessJWT, session.DID, true
}

func atprotoPublishPost(client *http.Client, pdsURL, accessJWT, did string, result *atprotoResult) bool {
	record := map[string]interface{}{
		"$type":     "app.bsky.feed.post",
		"text":      fmt.Sprintf("Vidra Federation Test [%s]", time.Now().UTC().Format(time.RFC3339)),
		"createdAt": time.Now().UTC().Format(time.RFC3339),
		"embed": map[string]interface{}{
			"$type": "app.bsky.embed.external",
			"external": map[string]interface{}{
				"uri":         "https://github.com/nickvidal/vidra",
				"title":       "Vidra Core - Federation Test",
				"description": "Automated federation verification post",
			},
		},
	}

	body, _ := json.Marshal(map[string]interface{}{
		"repo":       did,
		"collection": "app.bsky.feed.post",
		"record":     record,
		"validate":   true,
	})

	resp, err := doPost(client, pdsURL+"/xrpc/com.atproto.repo.createRecord", body, accessJWT)
	if err != nil {
		fmt.Printf("[FAIL] Post publish failed: %v\n", err)
		result.failed++
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Printf("[FAIL] Post publish failed (HTTP %d): %s\n", resp.StatusCode, string(respBody))
		result.failed++
		return false
	}

	var ref struct {
		URI string `json:"uri"`
		CID string `json:"cid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ref); err != nil {
		fmt.Printf("[FAIL] Post publish response decode failed: %v\n", err)
		result.failed++
		return false
	}

	result.postURI = ref.URI
	result.postCID = ref.CID
	fmt.Printf("[PASS] Test post published (%s)\n", ref.URI)
	result.passed++
	return true
}

func atprotoFetchPost(client *http.Client, pdsURL string, result *atprotoResult) {
	// Parse at:// URI: at://did/collection/rkey
	parts := strings.SplitN(strings.TrimPrefix(result.postURI, "at://"), "/", 3)
	if len(parts) != 3 {
		fmt.Printf("[FAIL] Cannot parse post URI: %s\n", result.postURI)
		result.failed++
		return
	}

	url := fmt.Sprintf("%s/xrpc/com.atproto.repo.getRecord?repo=%s&collection=%s&rkey=%s",
		pdsURL, parts[0], parts[1], parts[2])

	resp, err := doGet(client, url, "")
	if err != nil {
		fmt.Printf("[FAIL] Post fetch failed: %v\n", err)
		result.failed++
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Printf("[FAIL] Post fetch failed (HTTP %d): %s\n", resp.StatusCode, string(respBody))
		result.failed++
		return
	}

	var record struct {
		URI   string                 `json:"uri"`
		CID   string                 `json:"cid"`
		Value map[string]interface{} `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&record); err != nil {
		fmt.Printf("[FAIL] Post fetch decode failed: %v\n", err)
		result.failed++
		return
	}

	if record.URI != result.postURI {
		fmt.Printf("[FAIL] Post URI mismatch: got %s, want %s\n", record.URI, result.postURI)
		result.failed++
		return
	}

	text, _ := record.Value["text"].(string)
	if !strings.HasPrefix(text, "Vidra Federation Test") {
		fmt.Printf("[FAIL] Post text mismatch: %q\n", text)
		result.failed++
		return
	}

	fmt.Println("[PASS] Post fetched back successfully — text matches")
	result.passed++
}

func atprotoCheckFeed(client *http.Client, pdsURL, did string, result *atprotoResult) {
	url := fmt.Sprintf("%s/xrpc/app.bsky.feed.getAuthorFeed?actor=%s&limit=5", pdsURL, did)

	resp, err := doGet(client, url, "")
	if err != nil {
		fmt.Printf("[FAIL] Author feed fetch failed: %v\n", err)
		result.failed++
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Printf("[FAIL] Author feed fetch failed (HTTP %d): %s\n", resp.StatusCode, string(respBody))
		result.failed++
		return
	}

	var feed struct {
		Feed []struct {
			Post struct {
				URI string `json:"uri"`
			} `json:"post"`
		} `json:"feed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&feed); err != nil {
		fmt.Printf("[FAIL] Author feed decode failed: %v\n", err)
		result.failed++
		return
	}

	for _, item := range feed.Feed {
		if item.Post.URI == result.postURI {
			fmt.Println("[PASS] Post appears in author feed")
			result.passed++
			return
		}
	}

	fmt.Println("[FAIL] Post not found in author feed")
	result.failed++
}

func atprotoDeletePost(client *http.Client, pdsURL, accessJWT, did, rkey string, result *atprotoResult) {
	body, _ := json.Marshal(map[string]string{
		"repo":       did,
		"collection": "app.bsky.feed.post",
		"rkey":       rkey,
	})

	resp, err := doPost(client, pdsURL+"/xrpc/com.atproto.repo.deleteRecord", body, accessJWT)
	if err != nil {
		fmt.Printf("[FAIL] Post cleanup failed: %v\n", err)
		result.failed++
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Printf("[FAIL] Post cleanup failed (HTTP %d): %s\n", resp.StatusCode, string(respBody))
		result.failed++
		return
	}

	fmt.Println("[PASS] Test post cleaned up")
	result.passed++
}

func printATProtoSummary(result *atprotoResult) {
	total := result.passed + result.failed
	fmt.Printf("\n%d of %d checks passed.\n", result.passed, total)
}

// extractRkey extracts the rkey from an at:// URI.
// e.g., at://did:plc:abc123/app.bsky.feed.post/xyz789 -> xyz789
func extractRkey(uri string) string {
	parts := strings.SplitN(strings.TrimPrefix(uri, "at://"), "/", 3)
	if len(parts) == 3 {
		return parts[2]
	}
	return ""
}

func doPost(client *http.Client, url string, body []byte, bearerToken string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	return client.Do(req)
}

func doGet(client *http.Client, url, bearerToken string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	return client.Do(req)
}
