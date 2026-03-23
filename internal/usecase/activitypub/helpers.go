package activitypub

import (
	"fmt"
	"net/url"
	"strings"

	"vidra-core/internal/domain"
)

func (s *Service) buildActorID(username string) string {
	return fmt.Sprintf("%s/users/%s", s.cfg.PublicBaseURL, username)
}

func (s *Service) extractUsernameFromURI(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("failed to parse URI: %w", err)
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "users" {
		return parts[1], nil
	}

	return "", fmt.Errorf("invalid actor URI format")
}

func (s *Service) extractVideoIDFromURI(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("failed to parse URI: %w", err)
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "videos" {
		return parts[1], nil
	}

	return "", fmt.Errorf("invalid video URI format")
}

func parseDuration(durationStr string) int {
	duration := 0
	if len(durationStr) < 3 || !strings.HasPrefix(durationStr, "PT") {
		return 0
	}

	durationStr = durationStr[2:]

	if idx := strings.Index(durationStr, "H"); idx > 0 {
		hours := 0
		_, _ = fmt.Sscanf(durationStr[:idx], "%d", &hours)
		duration += hours * 3600
		durationStr = durationStr[idx+1:]
	}

	if idx := strings.Index(durationStr, "M"); idx > 0 {
		minutes := 0
		_, _ = fmt.Sscanf(durationStr[:idx], "%d", &minutes)
		duration += minutes * 60
		durationStr = durationStr[idx+1:]
	}

	if idx := strings.Index(durationStr, "S"); idx > 0 {
		seconds := 0
		_, _ = fmt.Sscanf(durationStr[:idx], "%d", &seconds)
		duration += seconds
	}

	return duration
}

func extractVideoURL(videoObj map[string]interface{}) string {
	if urls, ok := videoObj["url"].([]interface{}); ok {
		for _, u := range urls {
			if urlObj, ok := u.(map[string]interface{}); ok {
				if mediaType, ok := urlObj["mediaType"].(string); ok && mediaType == "video/mp4" {
					if href, ok := urlObj["href"].(string); ok {
						return href
					}
				}
			}
		}
		for _, u := range urls {
			if urlObj, ok := u.(map[string]interface{}); ok {
				if mediaType, ok := urlObj["mediaType"].(string); ok && strings.HasPrefix(mediaType, "video/") {
					if href, ok := urlObj["href"].(string); ok {
						return href
					}
				}
			}
		}
	}

	if url, ok := videoObj["url"].(string); ok {
		return url
	}

	return ""
}

func extractThumbnailURL(videoObj map[string]interface{}) string {
	if icon, ok := videoObj["icon"].(map[string]interface{}); ok {
		if url, ok := icon["url"].(string); ok {
			return url
		}
	}

	if image, ok := videoObj["image"].(map[string]interface{}); ok {
		if url, ok := image["url"].(string); ok {
			return url
		}
	}

	if preview, ok := videoObj["preview"].(map[string]interface{}); ok {
		if url, ok := preview["url"].(string); ok {
			return url
		}
	}

	return ""
}

func extractDomain(uri string) string {
	parsedURL, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	return parsedURL.Host
}

func determinePrivacy(videoObj map[string]interface{}) domain.Privacy {
	to := []string{}
	if toField, ok := videoObj["to"].([]interface{}); ok {
		for _, t := range toField {
			if str, ok := t.(string); ok {
				to = append(to, str)
			}
		}
	}

	cc := []string{}
	if ccField, ok := videoObj["cc"].([]interface{}); ok {
		for _, c := range ccField {
			if str, ok := c.(string); ok {
				cc = append(cc, str)
			}
		}
	}

	for _, t := range to {
		if t == ActivityPubPublic || t == "Public" || t == "as:Public" {
			return domain.PrivacyPublic
		}
	}

	for _, c := range cc {
		if c == ActivityPubPublic || c == "Public" || c == "as:Public" {
			return domain.PrivacyUnlisted
		}
	}

	return domain.PrivacyPrivate
}

func extractTags(videoObj map[string]interface{}) []string {
	tags := []string{}

	if tagField, ok := videoObj["tag"].([]interface{}); ok {
		for _, t := range tagField {
			if tagObj, ok := t.(map[string]interface{}); ok {
				if tagName, ok := tagObj["name"].(string); ok {
					tagName = strings.TrimPrefix(tagName, "#")
					tags = append(tags, tagName)
				}
			} else if tagStr, ok := t.(string); ok {
				tags = append(tags, strings.TrimPrefix(tagStr, "#"))
			}
		}
	}

	return tags
}
