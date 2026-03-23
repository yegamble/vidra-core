package security

import (
	"fmt"

	"github.com/microcosm-cc/bluemonday"
)

// SanitizeHTML sanitizes user-generated HTML content to prevent XSS attacks
// Uses bluemonday strict policy: only plain text allowed, all HTML tags removed
func SanitizeHTML(input string) string {
	if input == "" {
		return ""
	}

	p := bluemonday.StrictPolicy()
	return p.Sanitize(input)
}

// SanitizeHTMLWithBasicFormatting allows basic formatting tags (bold, italic, links)
// while preventing XSS attacks
func SanitizeHTMLWithBasicFormatting(input string) string {
	if input == "" {
		return ""
	}

	p := bluemonday.UGCPolicy() // User Generated Content policy
	return p.Sanitize(input)
}

// SanitizeCommentHTML sanitizes comment content allowing basic formatting
// Allows: bold, italic, links, lists, quotes
// Blocks: scripts, styles, iframes, forms, event handlers, javascript: URLs, etc.
func SanitizeCommentHTML(input string) string {
	if input == "" {
		return ""
	}

	// Create a custom policy based on UGC policy
	p := bluemonday.UGCPolicy()

	// Additional restrictions for comments
	// Force all links to have rel="nofollow" to prevent SEO spam
	p.RequireNoFollowOnLinks(true)

	// Require all links to have rel="noreferrer" for privacy
	p.RequireNoReferrerOnLinks(true)

	// Open all links in new window/tab
	p.AddTargetBlankToFullyQualifiedLinks(true)

	// Allow only https and http links
	p.AllowURLSchemes("http", "https")

	// Remove any data: or javascript: URLs
	p.RequireParseableURLs(true)

	return p.Sanitize(input)
}

// SanitizeStrictText removes ALL HTML tags and returns plain text only
// This is the most restrictive sanitization level
func SanitizeStrictText(input string) string {
	if input == "" {
		return ""
	}

	// StrictPolicy removes all HTML
	p := bluemonday.StrictPolicy()
	return p.Sanitize(input)
}

// SanitizeMarkdown sanitizes markdown-generated HTML
// Allows common markdown elements but blocks dangerous content
func SanitizeMarkdown(input string) string {
	if input == "" {
		return ""
	}

	// Create a policy for markdown content
	p := bluemonday.NewPolicy()

	// Headers
	p.AllowElements("h1", "h2", "h3", "h4", "h5", "h6")

	// Text formatting
	p.AllowElements("strong", "b", "em", "i", "u", "strike", "del", "code", "pre")

	// Lists
	p.AllowElements("ul", "ol", "li")

	// Quotes
	p.AllowElements("blockquote", "q")

	// Links
	p.AllowElements("a")
	p.AllowAttrs("href").OnElements("a")
	p.AllowURLSchemes("http", "https")
	p.RequireNoFollowOnLinks(true)
	p.RequireNoReferrerOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)

	// Paragraphs and breaks
	p.AllowElements("p", "br", "hr")

	// Tables
	p.AllowElements("table", "thead", "tbody", "tfoot", "tr", "th", "td")
	p.AllowAttrs("align").OnElements("th", "td")

	// Images (with restrictions)
	p.AllowElements("img")
	p.AllowAttrs("src", "alt", "title").OnElements("img")
	p.AllowURLSchemes("http", "https")
	p.RequireParseableURLs(true)

	// No scripts, iframes, forms, or event handlers allowed

	return p.Sanitize(input)
}

// SanitizeWithCustomPolicy allows creating a custom sanitization policy
// for specific use cases
type CustomSanitizerOptions struct {
	AllowImages     bool
	AllowLinks      bool
	AllowFormatting bool
	AllowLists      bool
	AllowTables     bool
	MaxLinkLength   int
	RequireNoFollow bool
	RequireHTTPS    bool
}

// SanitizeWithCustomPolicy sanitizes HTML with custom options
func SanitizeWithCustomPolicy(input string, opts CustomSanitizerOptions) string {
	if input == "" {
		return ""
	}

	p := bluemonday.NewPolicy()

	// Basic text elements
	p.AllowElements("p", "br", "span")

	if opts.AllowFormatting {
		p.AllowElements("strong", "b", "em", "i", "u", "code")
	}

	if opts.AllowLinks {
		p.AllowElements("a")
		p.AllowAttrs("href").OnElements("a")

		if opts.RequireHTTPS {
			p.AllowURLSchemes("https")
		} else {
			p.AllowURLSchemes("http", "https")
		}

		if opts.RequireNoFollow {
			p.RequireNoFollowOnLinks(true)
			p.RequireNoReferrerOnLinks(true)
		}

		p.AddTargetBlankToFullyQualifiedLinks(true)
		p.RequireParseableURLs(true)
	}

	if opts.AllowImages {
		p.AllowElements("img")
		p.AllowAttrs("src", "alt", "title").OnElements("img")

		if opts.RequireHTTPS {
			p.AllowURLSchemes("https")
		} else {
			p.AllowURLSchemes("http", "https")
		}

		p.RequireParseableURLs(true)
	}

	if opts.AllowLists {
		p.AllowElements("ul", "ol", "li")
	}

	if opts.AllowTables {
		p.AllowElements("table", "thead", "tbody", "tfoot", "tr", "th", "td")
		p.AllowAttrs("align").OnElements("th", "td")
	}

	return p.Sanitize(input)
}

// ValidateAndSanitizeLength validates string length and sanitizes HTML
func ValidateAndSanitizeLength(input string, maxLength int, sanitizer func(string) string) (string, error) {
	if len(input) > maxLength {
		return "", ErrContentTooLong
	}

	sanitized := sanitizer(input)

	// Check length again after sanitization (it might have changed)
	if len(sanitized) > maxLength {
		return "", ErrContentTooLong
	}

	return sanitized, nil
}

// Common errors for HTML sanitization
var (
	ErrContentTooLong = fmt.Errorf("content exceeds maximum allowed length")
)
