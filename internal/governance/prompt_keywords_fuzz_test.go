package governance

import (
	"regexp"
	"strings"
	"testing"
)

func FuzzPromptKeywordMatch(f *testing.F) {
	f.Add("literal", "secret", "has secret")
	f.Add("regex", "(?i)token", "TOKEN")
	f.Add("regex", `(a+)+$`, strings.Repeat("a", 64)+"b")
	f.Fuzz(func(t *testing.T, kind, pattern, text string) {
		if len(pattern) > 1024 || len(text) > 8192 {
			return
		}
		var compiled *regexp.Regexp
		if kind == "regex" {
			var err error
			compiled, err = regexp.Compile(pattern)
			if err != nil {
				if matchPromptKeyword(kind, pattern, nil, text) {
					t.Fatal("invalid pattern matched")
				}
				return
			}
		}
		_ = matchPromptKeyword(kind, pattern, compiled, text)
	})
}
