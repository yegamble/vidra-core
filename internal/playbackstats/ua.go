package playbackstats

import "strings"

// Closed vocabularies. The raw User-Agent never leaves ClassifyUA and is never
// stored; only these coarse buckets are.
const (
	DeviceMobile  = "mobile"
	DeviceDesktop = "desktop"
	DeviceTV      = "tv"
	DeviceOther   = "other"
)

// ClassifyUA buckets a User-Agent into a device class and an OS family. It is
// deliberately coarse (no browser, no version): enough to answer "do my viewers
// watch on phones", too little to fingerprint anybody.
func ClassifyUA(ua string) (device, osFamily string) {
	l := strings.ToLower(ua)
	if l == "" {
		return DeviceOther, "other"
	}
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(l, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("bot", "crawler", "spider", "curl/", "wget/"):
		return DeviceOther, "other"
	case has("smart-tv", "smarttv", "tizen", "web0s", "webos", "appletv", "roku", "crkey", "bravia", "aftt", "aftm", "hbbtv"):
		return DeviceTV, osOf(has)
	case has("iphone", "ipod", "ipad"):
		return DeviceMobile, "ios"
	case has("android"):
		return DeviceMobile, "android"
	case has("mobi"):
		return DeviceMobile, osOf(has)
	}
	return DeviceDesktop, osOf(has)
}

func osOf(has func(...string) bool) string {
	switch {
	case has("android"):
		return "android"
	case has("cros"):
		return "chromeos"
	case has("windows"):
		return "windows"
	case has("mac os x", "macintosh"):
		return "macos"
	case has("linux", "x11"):
		return "linux"
	}
	return "other"
}

var (
	deviceKeys = map[string]bool{DeviceMobile: true, DeviceDesktop: true, DeviceTV: true, DeviceOther: true}
	osKeys     = map[string]bool{"ios": true, "android": true, "windows": true, "macos": true, "linux": true, "chromeos": true, "other": true}
)
