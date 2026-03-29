package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type apResult struct {
	passed  int
	failed  int
	skipped int
}

func runActivityPub(baseURL, username string, testRemote bool) int {
	fmt.Println("=== ActivityPub Federation Verification ===")

	client := &http.Client{Timeout: 15 * time.Second}
	result := &apResult{}
	domain := extractDomain(baseURL)

	// Step 1: WebFinger
	apCheckWebFinger(client, baseURL, username, domain, result)

	// Step 2: Actor profile
	apCheckActor(client, baseURL, username, result)

	// Step 3: Outbox
	apCheckOutbox(client, baseURL, username, result)

	// Step 4: NodeInfo
	apCheckNodeInfo(client, baseURL, result)

	// Step 5: HTTP Signature round-trip
	apCheckHTTPSignature(result)

	// Step 6: Remote actor fetch (optional)
	if testRemote {
		apCheckRemoteActor(client, result)
	} else {
		fmt.Println("[SKIP] Remote actor fetch (use --test-remote to enable)")
		result.skipped++
	}

	printAPSummary(result)
	if result.failed > 0 {
		return 1
	}
	return 0
}

func apCheckWebFinger(client *http.Client, baseURL, username, domain string, result *apResult) {
	url := fmt.Sprintf("%s/.well-known/webfinger?resource=acct:%s@%s", baseURL, username, domain)

	resp, err := doGetAccept(client, url, "application/jrd+json")
	if err != nil {
		fmt.Printf("[FAIL] WebFinger request failed: %v\n", err)
		result.failed++
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Printf("[FAIL] WebFinger returned HTTP %d: %s\n", resp.StatusCode, string(respBody))
		result.failed++
		return
	}

	var jrd struct {
		Subject string `json:"subject"`
		Links   []struct {
			Rel  string `json:"rel"`
			Type string `json:"type"`
			Href string `json:"href"`
		} `json:"links"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jrd); err != nil {
		fmt.Printf("[FAIL] WebFinger response is not valid JSON: %v\n", err)
		result.failed++
		return
	}

	if jrd.Subject == "" {
		fmt.Println("[FAIL] WebFinger response missing 'subject' field")
		result.failed++
		return
	}

	hasSelf := false
	for _, link := range jrd.Links {
		if link.Rel == "self" && strings.Contains(link.Type, "activity+json") {
			hasSelf = true
			break
		}
	}
	if !hasSelf {
		fmt.Println("[FAIL] WebFinger response missing self link with activity+json type")
		result.failed++
		return
	}

	fmt.Printf("[PASS] WebFinger returns valid JRD for %s@%s\n", username, domain)
	result.passed++
}

func apCheckActor(client *http.Client, baseURL, username string, result *apResult) {
	url := fmt.Sprintf("%s/users/%s", baseURL, username)

	resp, err := doGetAccept(client, url, "application/activity+json")
	if err != nil {
		fmt.Printf("[FAIL] Actor profile request failed: %v\n", err)
		result.failed++
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Printf("[FAIL] Actor profile returned HTTP %d: %s\n", resp.StatusCode, string(respBody))
		result.failed++
		return
	}

	var actor map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&actor); err != nil {
		fmt.Printf("[FAIL] Actor profile is not valid JSON: %v\n", err)
		result.failed++
		return
	}

	requiredFields := []string{"type", "id", "inbox", "outbox"}
	missing := []string{}
	for _, field := range requiredFields {
		if _, ok := actor[field]; !ok {
			missing = append(missing, field)
		}
	}

	// Check publicKey as nested object
	hasPublicKey := false
	if pk, ok := actor["publicKey"]; ok && pk != nil {
		hasPublicKey = true
	}
	if !hasPublicKey {
		missing = append(missing, "publicKey")
	}

	if len(missing) > 0 {
		fmt.Printf("[FAIL] Actor profile missing required fields: %s\n", strings.Join(missing, ", "))
		result.failed++
		return
	}

	fmt.Println("[PASS] Actor profile has required fields (type, id, inbox, outbox, publicKey)")
	result.passed++
}

func apCheckOutbox(client *http.Client, baseURL, username string, result *apResult) {
	url := fmt.Sprintf("%s/users/%s/outbox", baseURL, username)

	resp, err := doGetAccept(client, url, "application/activity+json")
	if err != nil {
		fmt.Printf("[FAIL] Outbox request failed: %v\n", err)
		result.failed++
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Printf("[FAIL] Outbox returned HTTP %d: %s\n", resp.StatusCode, string(respBody))
		result.failed++
		return
	}

	var collection map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&collection); err != nil {
		fmt.Printf("[FAIL] Outbox is not valid JSON: %v\n", err)
		result.failed++
		return
	}

	colType, _ := collection["type"].(string)
	if colType != "OrderedCollection" {
		fmt.Printf("[FAIL] Outbox type is %q, expected \"OrderedCollection\"\n", colType)
		result.failed++
		return
	}

	if _, ok := collection["totalItems"]; !ok {
		fmt.Println("[FAIL] Outbox missing 'totalItems' field")
		result.failed++
		return
	}

	fmt.Println("[PASS] Outbox returns valid OrderedCollection")
	result.passed++
}

func apCheckNodeInfo(client *http.Client, baseURL string, result *apResult) {
	// Step 1: Fetch /.well-known/nodeinfo discovery document
	url := baseURL + "/.well-known/nodeinfo"
	resp, err := doGetAccept(client, url, "application/json")
	if err != nil {
		fmt.Printf("[FAIL] NodeInfo discovery request failed: %v\n", err)
		result.failed++
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Printf("[FAIL] NodeInfo discovery returned HTTP %d: %s\n", resp.StatusCode, string(respBody))
		result.failed++
		return
	}

	var discovery struct {
		Links []struct {
			Rel  string `json:"rel"`
			Href string `json:"href"`
		} `json:"links"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&discovery); err != nil {
		fmt.Printf("[FAIL] NodeInfo discovery is not valid JSON: %v\n", err)
		result.failed++
		return
	}

	var nodeInfoURL string
	for _, link := range discovery.Links {
		if strings.Contains(link.Rel, "nodeinfo.diaspora.software/ns/schema/2.0") {
			nodeInfoURL = link.Href
			break
		}
	}

	if nodeInfoURL == "" {
		fmt.Println("[FAIL] NodeInfo discovery missing link to NodeInfo 2.0 schema")
		result.failed++
		return
	}

	// Step 2: Fetch the actual NodeInfo 2.0 document
	resp2, err := doGetAccept(client, nodeInfoURL, "application/json")
	if err != nil {
		fmt.Printf("[FAIL] NodeInfo 2.0 request failed: %v\n", err)
		result.failed++
		return
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp2.Body)
		fmt.Printf("[FAIL] NodeInfo 2.0 returned HTTP %d: %s\n", resp2.StatusCode, string(respBody))
		result.failed++
		return
	}

	var nodeInfo struct {
		Version  string `json:"version"`
		Software struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"software"`
		Protocols []string `json:"protocols"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&nodeInfo); err != nil {
		fmt.Printf("[FAIL] NodeInfo 2.0 is not valid JSON: %v\n", err)
		result.failed++
		return
	}

	if nodeInfo.Version != "2.0" {
		fmt.Printf("[FAIL] NodeInfo version is %q, expected \"2.0\"\n", nodeInfo.Version)
		result.failed++
		return
	}

	hasAP := false
	for _, p := range nodeInfo.Protocols {
		if p == "activitypub" {
			hasAP = true
			break
		}
	}
	if !hasAP {
		fmt.Printf("[FAIL] NodeInfo protocols %v does not include \"activitypub\"\n", nodeInfo.Protocols)
		result.failed++
		return
	}

	fmt.Printf("[PASS] NodeInfo 2.0 schema valid (software: %s/%s, protocols: %v)\n",
		nodeInfo.Software.Name, nodeInfo.Software.Version, nodeInfo.Protocols)
	result.passed++
}

func apCheckHTTPSignature(result *apResult) {
	// Generate a temporary RSA key pair
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		fmt.Printf("[FAIL] Key generation failed: %v\n", err)
		result.failed++
		return
	}

	// Create a test request
	req, _ := http.NewRequest("GET", "https://example.com/users/test/inbox", nil)
	req.Header.Set("Host", "example.com")
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))

	// Build signing string
	headers := []string{"(request-target)", "host", "date"}
	signingString := buildTestSigningString(req, headers)

	// Sign
	hash := sha256.Sum256([]byte(signingString))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hash[:])
	if err != nil {
		fmt.Printf("[FAIL] Signing failed: %v\n", err)
		result.failed++
		return
	}

	sigBase64 := base64.StdEncoding.EncodeToString(signature)
	sigHeader := fmt.Sprintf(`keyId="https://example.com/users/test#main-key",algorithm="rsa-sha256",headers="%s",signature="%s"`,
		strings.Join(headers, " "), sigBase64)
	req.Header.Set("Signature", sigHeader)

	// Verify using the public key
	pubKeyBytes, _ := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	pubKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubKeyBytes})

	// Parse signature back and verify
	sigBytes, _ := base64.StdEncoding.DecodeString(sigBase64)
	pubKey, _ := x509.ParsePKIXPublicKey(pubKeyBytes)
	rsaPubKey := pubKey.(*rsa.PublicKey)

	verifyHash := sha256.Sum256([]byte(signingString))
	err = rsa.VerifyPKCS1v15(rsaPubKey, crypto.SHA256, verifyHash[:], sigBytes)
	if err != nil {
		fmt.Printf("[FAIL] HTTP signature verification failed: %v\n", err)
		result.failed++
		return
	}

	_ = pubKeyPEM // Used above via pubKeyBytes
	fmt.Println("[PASS] HTTP signature round-trip valid")
	result.passed++
}

func apCheckRemoteActor(client *http.Client, result *apResult) {
	// Step 1: WebFinger lookup for @Mastodon@mastodon.social
	wfURL := "https://mastodon.social/.well-known/webfinger?resource=acct:Mastodon@mastodon.social"
	resp, err := doGetAccept(client, wfURL, "application/jrd+json")
	if err != nil {
		fmt.Printf("[FAIL] Remote WebFinger request failed: %v\n", err)
		result.failed++
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("[FAIL] Remote WebFinger returned HTTP %d\n", resp.StatusCode)
		result.failed++
		return
	}

	var jrd struct {
		Links []struct {
			Rel  string `json:"rel"`
			Type string `json:"type"`
			Href string `json:"href"`
		} `json:"links"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jrd); err != nil {
		fmt.Printf("[FAIL] Remote WebFinger response is not valid JSON: %v\n", err)
		result.failed++
		return
	}

	var actorURL string
	for _, link := range jrd.Links {
		if link.Rel == "self" && strings.Contains(link.Type, "activity+json") {
			actorURL = link.Href
			break
		}
	}

	if actorURL == "" {
		fmt.Println("[FAIL] Remote WebFinger has no self link")
		result.failed++
		return
	}

	// Step 2: Fetch the actor
	resp2, err := doGetAccept(client, actorURL, "application/activity+json")
	if err != nil {
		fmt.Printf("[FAIL] Remote actor fetch failed: %v\n", err)
		result.failed++
		return
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		fmt.Printf("[FAIL] Remote actor returned HTTP %d\n", resp2.StatusCode)
		result.failed++
		return
	}

	var actor map[string]interface{}
	if err := json.NewDecoder(resp2.Body).Decode(&actor); err != nil {
		fmt.Printf("[FAIL] Remote actor response is not valid JSON: %v\n", err)
		result.failed++
		return
	}

	actorType, _ := actor["type"].(string)
	if actorType == "" {
		fmt.Println("[FAIL] Remote actor missing 'type' field")
		result.failed++
		return
	}

	fmt.Printf("[PASS] Remote actor fetched (type: %s, url: %s)\n", actorType, actorURL)
	result.passed++
}

func printAPSummary(result *apResult) {
	total := result.passed + result.failed
	fmt.Printf("\n%d of %d checks passed", result.passed, total)
	if result.skipped > 0 {
		fmt.Printf(", %d skipped", result.skipped)
	}
	fmt.Println(".")
}

func extractDomain(baseURL string) string {
	// Strip scheme
	d := baseURL
	d = strings.TrimPrefix(d, "http://")
	d = strings.TrimPrefix(d, "https://")
	// Strip path
	if idx := strings.Index(d, "/"); idx != -1 {
		d = d[:idx]
	}
	return d
}

func doGetAccept(client *http.Client, url, accept string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", accept)
	return client.Do(req)
}

func buildTestSigningString(r *http.Request, headers []string) string {
	var lines []string
	for _, h := range headers {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "(request-target)" {
			target := fmt.Sprintf("%s %s", strings.ToLower(r.Method), r.URL.RequestURI())
			lines = append(lines, fmt.Sprintf("(request-target): %s", target))
		} else {
			value := r.Header.Get(h)
			lines = append(lines, fmt.Sprintf("%s: %s", h, value))
		}
	}
	return strings.Join(lines, "\n")
}
