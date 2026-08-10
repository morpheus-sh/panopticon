package engine

import (
	"regexp"
	"strings"
)

// stripANSI removes ANSI/VT escape sequences for plain-text matching.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*\x07|\x1b[>=]|\x1b\\`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// regexCache caches compiled regexes for matchesRegex.
var regCache = map[string]*regexp.Regexp{}

func matchesRegex(haystack, pattern string) bool {
	re, ok := regCache[pattern]
	if !ok {
		re = regexp.MustCompile(pattern)
		regCache[pattern] = re
	}
	return re.MatchString(haystack)
}

// diff returns the tail of b after the longest common prefix with a,
// used for lightweight delta streaming.
func diff(prev, curr string) string {
	n := len(prev)
	if n > len(curr) {
		n = len(curr)
	}
	i := 0
	for i < n && prev[i] == curr[i] {
		i++
	}
	return curr[i:]
}

// quoteShell quotes a path for safe interpolation into a shell-typed tmux
// command.
func quoteShell(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// cwdOr returns dir with a sensible fallback.
func cwdOr(cwd, fallback string) string {
	if cwd == "" {
		return fallback
	}
	return cwd
}
