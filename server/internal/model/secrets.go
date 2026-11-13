package model

import (
	"regexp"
	"strings"
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*(PRIVATE KEY|CERTIFICATE)[A-Z0-9 ]*-----`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}\b`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{30,}\b`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{24,}\b`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}\b`),
	regexp.MustCompile(`\bwl_[a-z]_[A-Za-z0-9_-]{30,}`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}`),
	regexp.MustCompile(`://[^/\s:@]+:[^\s@]+@`),
}

// LooksSecret reports whether a string appears to contain a secret value.
func LooksSecret(s string) bool {
	for _, re := range secretPatterns {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// Scrub prepares text from a collector for storage: anything secret-shaped is
// replaced outright, and the rest is cut to a sane length. A collector error can
// quote a command line, and a command line can quote a token.
func Scrub(s string, limit int) string {
	if LooksSecret(s) {
		return "[已拦截疑似秘密内容]"
	}
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit])
	}
	return s
}

var (
	logKeyValue = regexp.MustCompile(`(?i)\b(pass(?:word|wd)?|pwd|secret|token|api[_-]?key|access[_-]?key|authorization|cookie|session(?:id)?)\b(\s*[=:]\s*|\s+)("[^"]*"|'[^']*'|\S+)`)
	logMasks    = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`),
		regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+\b`),
		regexp.MustCompile(`\b[A-Fa-f0-9]{32,}\b`),
		regexp.MustCompile(`[A-Za-z0-9+/_-]{40,}={0,2}`),
	}
)

// RedactLog prepares one log line for storage. Unlike Scrub it keeps the line, since
// a log with holes still locates a fault, and masks what looks like a credential, a
// long opaque value or a mail address. It is a filter of best effort, which is why log
// text is also gated by its own scope and by the workspace cloud policy.
func RedactLog(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f:
			return -1
		}
		return r
	}, s)
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, "[已隐去]")
	}
	s = logKeyValue.ReplaceAllString(s, "$1$2[已隐去]")
	for _, re := range logMasks {
		s = re.ReplaceAllString(s, "[已隐去]")
	}
	if r := []rune(s); len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}

var scpForm = regexp.MustCompile(`^(?:[^@/\s]+@)?([^:/\s]{2,}):([^\s]+)$`)

// ParseRemote splits a git remote into host and path, dropping every trace of
// credentials. The authority is cut at its last @ by hand: url.Parse reads a # in a
// password as a fragment and hands back the password as the host. Anything that
// cannot be read with confidence returns ok=false rather than a partial answer.
func ParseRemote(raw string) (host, path string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	if i := strings.Index(raw, "://"); i > 0 {
		auth, rest, _ := strings.Cut(raw[i+3:], "/")
		if at := strings.LastIndexByte(auth, '@'); at >= 0 {
			auth = auth[at+1:]
		}
		// A password containing a slash leaves its tail, and the @, in the path.
		if strings.Contains(rest, "@") {
			return "", "", false
		}
		rest, _, _ = strings.Cut(rest, "?")
		rest, _, _ = strings.Cut(rest, "#")
		if c := strings.LastIndexByte(auth, ':'); c >= 0 {
			auth = auth[:c]
		}
		if auth == "" || LooksSecret(rest) {
			return "", "", false
		}
		return auth, rest, true
	}
	m := scpForm.FindStringSubmatch(raw)
	if m == nil || strings.Contains(m[2], "@") {
		return "", "", false
	}
	path, _, _ = strings.Cut(m[2], "?")
	path, _, _ = strings.Cut(path, "#")
	if LooksSecret(path) {
		return "", "", false
	}
	return m[1], path, true
}
