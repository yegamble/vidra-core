package obs

import "net"

// AnonymizeIP anonymizes an IP address for privacy-preserving logging.
// For IPv4: zeros the last octet (192.168.1.100 → 192.168.1.0).
// For IPv6: zeros the last 80 bits (last 10 bytes).
// Matches PeerTube's anonymize_ip behavior.
// Invalid or empty inputs are returned as-is.
func AnonymizeIP(ip string) string {
	if ip == "" {
		return ""
	}

	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ip
	}

	// Check if it's IPv4
	if v4 := parsed.To4(); v4 != nil {
		// Zero the last octet
		v4[3] = 0
		return v4.String()
	}

	// IPv6: zero the last 80 bits (last 10 bytes out of 16)
	v6 := parsed.To16()
	for i := 6; i < 16; i++ {
		v6[i] = 0
	}
	return v6.String()
}
