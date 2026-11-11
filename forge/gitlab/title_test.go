package gitlab

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lesomnus/forge-mirror/forge"
)

// GitLab refuses a title over 255 characters, and GitHub does not. A longer one
// is cut, counted in characters so a multibyte title is not cut mid-rune.
func TestTitleOfFitsGitLab(t *testing.T) {
	for _, tc := range []struct {
		what  string
		title string
		want  int // length in runes
		cut   bool
	}{
		{"short", "fix: a thing", 12, false},
		{"exactly the limit", strings.Repeat("a", 255), 255, false},
		{"one over", strings.Repeat("a", 256), 255, true},
		{"multibyte over", strings.Repeat("가", 300), 255, true},
	} {
		got := titleOf(forge.Issue{Title: tc.title})
		if !utf8.ValidString(got) {
			t.Errorf("%s: cut mid-rune: %q", tc.what, got)
		}
		if n := utf8.RuneCountInString(got); n != tc.want {
			t.Errorf("%s: %d characters; want %d", tc.what, n, tc.want)
		}
		if strings.HasSuffix(got, "…") != tc.cut {
			t.Errorf("%s: ends in an ellipsis = %v; want %v", tc.what, !tc.cut, tc.cut)
		}
	}
}
