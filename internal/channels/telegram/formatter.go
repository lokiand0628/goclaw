package telegram

import (
	"strings"
)

// FormatMessage converts standard Markdown input into Telegram MarkdownV2 compliant format.
func FormatMessage(text string) string {
	if text == "" {
		return ""
	}

	var b strings.Builder
	runes := []rune(text)
	length := len(runes)
	i := 0

	for i < length {
		// 1. Check for Code Block ```
		if i+2 < length && string(runes[i:i+3]) == "```" {
			// Find end of code block
			end := -1
			// Skip the opening ```
			j := i + 3
			for j+2 < length {
				if string(runes[j:j+3]) == "```" {
					end = j
					break
				}
				j++
			}

			if end != -1 {
				// We found a complete code block.
				// Extract raw content including the delimiters for now as we reconstruct it manually.
				// Actually, let's parse it properly.

				// Output opening ```
				b.WriteString("```")

				// Check for language identifier (until newline)
				langStart := i + 3
				contentStart := langStart

				// Find first newline to separate language from content
				for k := langStart; k < end; k++ {
					if runes[k] == '\n' {
						contentStart = k + 1
						// Append language identifier immediately (it doesn't need escaping usually, but let's be safe: only alphanumeric)
						// V2 docs say: "The language of the code block... can be specified...". It doesn't strictly say it needs escaping, but standard text rules apply?
						// Actually, inside pre entity, no formatting is parsed.
						b.WriteString(string(runes[langStart:contentStart])) // Include the newline
						break
					}
				}

				if contentStart == langStart {
					// No language specified or no newline found before end (inline triple backticks?)
					// Just treat everything from start as content
				}

				// Append content, escaping ` and \
				content := string(runes[contentStart:end])
				if contentStart > langStart {
					// We already appended the header
				} else {
					content = string(runes[langStart:end])
				}

				b.WriteString(escapeCode(content))
				b.WriteString("```")

				i = end + 3
				continue
			}
		}

		// 2. Check for Inline Code `
		if runes[i] == '`' {
			// Find closing `
			end := -1
			for j := i + 1; j < length; j++ {
				if runes[j] == '`' {
					end = j
					break
				}
			}

			if end != -1 {
				// Found inline code
				content := string(runes[i+1 : end])
				b.WriteString("`")
				b.WriteString(escapeCode(content))
				b.WriteString("`")
				i = end + 1
				continue
			}
		}

		// 3. Check for Bold ** -> Telegram *
		if i+1 < length && string(runes[i:i+2]) == "**" {
			// Find closing **
			end := -1
			for j := i + 2; j+1 < length; j++ {
				if string(runes[j:j+2]) == "**" {
					end = j
					break
				}
			}

			if end != -1 {
				content := string(runes[i+2 : end])
				// Recurse/Process inner content?
				// For simplicity and safety against nesting issues, let's simple-escape the inner content
				// unless we want to support nested links/code (which is complex).
				// Let's call a helper that only escapes text but preserves nothing else for now to be safe,
				// OR recurse FormatMessage? Recursion might cause issues if we don't consume input correctly.
				// Let's just escape text for now.
				b.WriteString("*")
				b.WriteString(escapeText(content))
				b.WriteString("*")
				i = end + 2
				continue
			}
		}

		// 4. Check for Link [text](url)
		if runes[i] == '[' {
			// Find closing ]
			closeBracket := -1
			for j := i + 1; j < length; j++ {
				if runes[j] == ']' {
					// Check if next char is (
					if j+1 < length && runes[j+1] == '(' {
						closeBracket = j
						break
					}
				}
			}

			if closeBracket != -1 {
				// Find closing )
				closeParen := -1
				for j := closeBracket + 2; j < length; j++ {
					if runes[j] == ')' {
						closeParen = j
						break
					}
				}

				if closeParen != -1 {
					textPart := string(runes[i+1 : closeBracket])
					urlPart := string(runes[closeBracket+2 : closeParen])

					b.WriteString("[")
					b.WriteString(escapeText(textPart))
					b.WriteString("](")
					b.WriteString(escapeURL(urlPart))
					b.WriteString(")")

					i = closeParen + 1
					continue
				}
			}
		}

		// 5. Normal Character
		if isSpecial(runes[i]) {
			b.WriteRune('\\')
		}
		b.WriteRune(runes[i])
		i++
	}

	return b.String()
}

// isSpecial checks if a rune needs escaping in Telegram MarkdownV2 (outside of code).
func isSpecial(r rune) bool {
	switch r {
	case '_', '*', '[', ']', '(', ')', '~', '`', '>', '#', '+', '-', '=', '|', '{', '}', '.', '!':
		return true
	}
	return false
}

// escapeText escapes all special characters. Used for content inside bold/italics or normal text.
func escapeText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if isSpecial(r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// escapeCode escapes characters required inside code blocks/inline code (` and \).
func escapeCode(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '`' || r == '\\' {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// escapeURL escapes characters required inside URL definition.
// Inside (...) part of a link, ')' and '\' must be escaped.
func escapeURL(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == ')' || r == '\\' {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
