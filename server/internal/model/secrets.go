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
