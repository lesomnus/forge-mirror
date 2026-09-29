package gitlab

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lesomnus/forge-mirror/forge"
)

// TestMergeableRefusesOneBranchAsBothEnds is the third ordinary reason a pull
// request cannot become a merge request, and the one a branch lookup cannot
// see: a fork's branch and the base can carry the same name, both names then
// exist in the target, and GitLab refuses source equal to target with a 422
// that `isConflict` does not match.
//
// Read off the issue rather than off the answer, because a 422 has other causes
// that are not ordinary.
func TestMergeableRefusesOneBranchAsBothEnds(t *testing.T) {
	for _, tc := range []struct {
		desc string
		head string
		base string
		want bool
	}{
		{"two branches", "feat/x", "main", true},
		{"a fork's branch named like the base", "feat/x", "feat/x", false},
		{"names that only differ by case are two refs to git", "Feat/X", "feat/x", true},
		{"empty is hasBranches's to refuse, not this one's", "", "main", true},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			require.Equal(t, tc.want, mergeable(forge.Issue{Head: tc.head, Base: tc.base}))
		})
	}
}
