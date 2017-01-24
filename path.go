package main

import (
	"os"
	"strings"
	"unicode"
)

var caseSensitive bool

func init() {
	wd, err := os.Getwd()
	if err != nil {
		return // panic ???
	}
	fi, err := os.Stat(wd)
	if err != nil {
		return // panic ???
	}

	// swap case
	var s []rune
	for _, r := range wd {
		switch {
		case unicode.IsUpper(r):
			s = append(s, unicode.ToLower(r))
		case unicode.IsLower(r):
			s = append(s, unicode.ToUpper(r))
		default:
			s = append(s, r)
		}
	}

	if dup, err := os.Stat(string(s)); err != nil || !os.SameFile(dup, fi) {
		caseSensitive = true
	}
}

// return len(s) >= len(prefix) && s[0:len(prefix)] == prefix
func hasPathPrefix(s, prefix string) bool {
	if caseSensitive {
		return len(s) >= len(prefix) && s[0:len(prefix)] == prefix
	}
	return len(s) >= len(prefix) && strings.EqualFold(s[0:len(prefix)], prefix)
}
