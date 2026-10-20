package gitlab

import (
	"testing"

	"github.com/stretchr/testify/require"
	gl "gitlab.com/gitlab-org/api/client-go"

	"github.com/lesomnus/forge-mirror/forge"
)

// TestMergeRequestStateEventOnlyForAChange locks in that an update sends no
// state event for the state a merge request is already in. GitLab 19.5 checks
// both branches before a reopen even when the merge request is open, so an
// unconditional `reopen` failed the repository whenever the target was gone
// (#21).
func TestMergeRequestStateEventOnlyForAChange(t *testing.T) {
	for _, tc := range []struct {
		desc string
		cur  string
		want forge.State
		ev   string
	}{
		{"open stays open", "opened", forge.StateOpen, ""},
		{"closed stays closed", "closed", forge.StateClosed, ""},
		{"closed at the source", "opened", forge.StateClosed, "close"},
		{"reopened at the source", "closed", forge.StateOpen, "reopen"},
		{"merged has no way back", "merged", forge.StateOpen, ""},
		{"merged is already shut", "merged", forge.StateClosed, ""},
		{"locked is GitLab's to release", "locked", forge.StateClosed, ""},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			got := mergeRequestStateEvent(tc.cur, tc.want)
			if tc.ev == "" {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tc.ev, *got)
		})
	}
}

// TestRetargetFollowsTheSourceBase is the other half of #21: a pull request
// retargeted at the source — GitHub does it when the base is merged and
// deleted — takes its merge request with it.
func TestRetargetFollowsTheSourceBase(t *testing.T) {
	cur := &gl.MergeRequest{}
	cur.SourceBranch = "feat/peg"
	cur.TargetBranch = "feat/hand"

	for _, tc := range []struct {
		desc string
		base string
		want bool
	}{
		{"the base moved", "dev", true},
		{"the base is where it was", "feat/hand", false},
		{"no base to follow", "", false},
		{"the merge request's own source is no target", "feat/peg", false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			i := forge.Issue{Kind: forge.KindPull, Head: "feat/peg", Base: tc.base}
			require.Equal(t, tc.want, retarget(cur, i))
		})
	}
}
