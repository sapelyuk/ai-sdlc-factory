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
// JWTs, PEM headers, DSN passwords, and OpenAI/Anthropic API keys.
var secretValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`), // GitHub fine-grained PATs
	regexp.MustCompile(`glpat-[A-Za-z0-9_-]{20,}`),     // GitLab PATs
	regexp.MustCompile(`xox[baprs]-[0-9A-Za-z-]{10,}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)[a-z0-9+.-]+://[^\s:@/]+:[^\s@/]+@`),
	regexp.MustCompile(`(?i)\b(?:password|passwd|secret|token|api_key|apikey)\b\s*[=:]\s*\S+`),
	// OpenAI / Anthropic API keys: more-specific prefixes first.
	regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`),  // Anthropic
	regexp.MustCompile(`\bsk-proj-[A-Za-z0-9_-]{20,}`), // OpenAI project
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`),      // OpenAI, Anthropic
}

// safeKeys are normalized key names that contain a word of the sensitive set
// but hold observability counters rather than secret material. Token usage and
// cost figures are the attribution contract of ADR-021, so redacting them would
// make the audit log useless for billing. The list is exact-match on purpose: an
// unknown key that merely contains "token" stays redacted.
var safeKeys = map[string]struct{}{
	"tokensin":         {},
	"tokensout":        {},
	"totaltokens":      {},
	"totaltokensin":    {},
	"totaltokensout":   {},
	"finaltokens":      {},
	"finaltokensin":    {},
	"finaltokensout":   {},
	"prompttokens":     {},
	"completiontokens": {},
	"cachedtokens":     {},
	"reasoningtokens":  {},
	"maxtokens":        {},
	"tokencount":       {},
	"tokensused":       {},
	"tokenscharged":    {},
}

// IsSensitiveKey reports whether a details key name denotes secret material.
func IsSensitiveKey(key string) bool {
	normalized := normalizeKey(key)
	if _, ok := safeKeys[normalized]; ok {
		return false
	}
	for _, part := range sensitiveKeyParts {
		if strings.Contains(normalized, part) {
			return true
		}
	}
	return false
}

// normalizeKey lower-cases a key and drops the separators used by the naming
// conventions of the services that write audit events.
func normalizeKey(key string) string {
	normalized := strings.ToLower(key)
	for _, sep := range []string{"_", "-", ".", " "} {
		normalized = strings.ReplaceAll(normalized, sep, "")
	}
	return normalized
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
