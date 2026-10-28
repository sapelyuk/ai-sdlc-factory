package audit

import (
	"regexp"
	"strings"
)

// RedactedValue replaces any secret material found in event details.
const RedactedValue = "[REDACTED]"

// maxRedactDepth bounds the recursive walk of a details payload. Audit details
// are shallow by contract; the limit keeps a malformed payload from turning
// redaction into a stack overflow.
const maxRedactDepth = 12

// sensitiveKeyParts are matched against normalized key names (lower-cased,
// with '_', '-', '.' and ' ' removed).
var sensitiveKeyParts = []string{
	"token",
	"secret",
	"password",
	"passwd",
	"credential",
	"apikey",
	"authorization",
	"authheader",
	"privatekey",
	"accesskey",
	"secretkey",
	"sessioncookie",
	"cookie",
	"bearer",
	"vault",
	"dsn",
	"connectionstring",
	"pin",
	"xkey",
}

// secretValuePatterns match well-known credential formats wherever they appear
// inside a string value: AWS access key IDs, GitHub tokens, Slack tokens,
// JWTs, PEM headers and DSN passwords.
var secretValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`xox[baprs]-[0-9A-Za-z-]{10,}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)[a-z0-9+.-]+://[^\s:@/]+:[^\s@/]+@`),
	regexp.MustCompile(`(?i)\b(?:password|passwd|secret|token|api_key|apikey)\b\s*[=:]\s*\S+`),
}

// IsSensitiveKey reports whether a details key name denotes secret material.
func IsSensitiveKey(key string) bool {
	normalized := strings.ToLower(key)
	for _, sep := range []string{"_", "-", ".", " "} {
		normalized = strings.ReplaceAll(normalized, sep, "")
	}
	for _, part := range sensitiveKeyParts {
		if strings.Contains(normalized, part) {
			return true
		}
	}
	return false
}

// RedactString replaces known credential formats inside a string.
func RedactString(s string) string {
	for _, re := range secretValuePatterns {
		s = re.ReplaceAllString(s, RedactedValue)
	}
	return s
}

// Redact returns a copy of details with secret material replaced by
// RedactedValue. The input map is never mutated. Keys that denote secrets are
// always redacted; string values are screened for known credential formats.
func Redact(details map[string]any) map[string]any {
	if details == nil {
		return nil
	}
	return redactMap(details, 0)
}

func redactMap(in map[string]any, depth int) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		if IsSensitiveKey(key) {
			out[key] = RedactedValue
			continue
		}
		out[key] = redactValue(value, depth+1)
	}
	return out
}

func redactValue(value any, depth int) any {
	if depth >= maxRedactDepth {
		return RedactedValue
	}
	switch v := value.(type) {
	case map[string]any:
		return redactMap(v, depth)
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, redactValue(item, depth+1))
		}
		return out
	case []string:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, redactValue(item, depth+1))
		}
		return out
	case string:
		return RedactString(v)
	default:
		return value
	}
}
