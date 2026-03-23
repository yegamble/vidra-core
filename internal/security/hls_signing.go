package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

// GenerateHLSToken creates an HMAC-SHA256 hex signature for path+exp
// path should be the relative HLS path under /api/v1/hls (e.g., "{videoId}/master.m3u8")
func GenerateHLSToken(secret, path string, exp int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(path))
	mac.Write([]byte{':'})
	mac.Write([]byte(strconv.FormatInt(exp, 10)))
	sum := mac.Sum(nil)
	return hex.EncodeToString(sum)
}

// VerifyHLSToken validates token and expiry
func VerifyHLSToken(secret, path string, exp int64, token string, now time.Time) bool {
	if exp <= now.Unix() {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(path))
	mac.Write([]byte{':'})
	mac.Write([]byte(strconv.FormatInt(exp, 10)))
	expected := mac.Sum(nil)
	dec, err := hex.DecodeString(token)
	if err != nil {
		return false
	}
	return hmac.Equal(expected, dec)
}
