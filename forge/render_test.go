package forge_test

import (
	"testing"
	"time"

	"github.com/lesomnus/forge-mirror/forge"
	"github.com/stretchr/testify/require"
)

func TestTargetKind(t *testing.T) {
	tcs := []struct {
		desc string
		in   forge.Issue
		want forge.Kind
		why  string
	}{
		{
			desc: "issue stays an issue",
			in:   forge.Issue{Kind: forge.KindIssue, State: forge.StateOpen},
			want: forge.KindIssue,
		},
		{
			desc: "closed issue stays an issue",
			in:   forge.Issue{Kind: forge.KindIssue, State: forge.StateClosed},
			want: forge.KindIssue,
		},
		{
			desc: "open pull request becomes a merge request",
			in:   forge.Issue{Kind: forge.KindPull, State: forge.StateOpen, Head: "fix/a", Base: "main"},
			want: forge.KindPull,
			why:  "the branch still exists, so a merge request can be made and is the useful form",
		},
		{
			desc: "closed pull request becomes an issue",
			in:   forge.Issue{Kind: forge.KindPull, State: forge.StateClosed, Head: "fix/a", Base: "main"},
			want: forge.KindIssue,
			why:  "the source branch is usually deleted on merge; a merge request cannot be created without it",
		},
	}
	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			require.Equal(t, tc.want, forge.TargetKind(tc.in), tc.why)
		})
	}
}

func TestIsStale(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)

	tcs := []struct {
		desc       string
		updatedAt  time.Time
		mirroredAt time.Time
		want       bool
	}{
		{
			desc:       "never mirrored",
			updatedAt:  t0,
			mirroredAt: time.Time{},
			want:       true,
		},
		{
			desc:       "changed after it was mirrored",
			updatedAt:  t0.Add(time.Hour),
			mirroredAt: t0,
			want:       true,
		},
		{
			desc:       "unchanged since it was mirrored",
			updatedAt:  t0,
			mirroredAt: t0,
			want:       false,
		},
		{
			desc:       "mirrored after the last change",
			updatedAt:  t0,
			mirroredAt: t0.Add(time.Hour),
			want:       false,
		},
	}
	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			got := forge.IsStale(forge.Issue{UpdatedAt: tc.updatedAt}, tc.mirroredAt)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestRenderNamesTheAuthor(t *testing.T) {
	// Authorship cannot cross: these people have no account on the target and
	// the program writes with one token. Naming them in the body is the whole
	// mitigation, so it is asserted rather than left to survive by luck.
	o := forge.Origin{Forge: "github", Repo: forge.Repo{Owner: "o", Name: "r"}, Number: 1, Kind: forge.KindIssue}

	body := forge.Render(forge.Issue{
		Kind:      forge.KindIssue,
		Author:    "octocat",
		CreatedAt: time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC),
		Body:      "text",
	}, o)

	require.Contains(t, body, "octocat")
	require.Contains(t, body, "2026-03-04")
}

func TestRenderPullShowsBranches(t *testing.T) {
	o := forge.Origin{Forge: "github", Repo: forge.Repo{Owner: "o", Name: "r"}, Number: 2, Kind: forge.KindPull}

	body := forge.Render(forge.Issue{
		Kind: forge.KindPull,
		Head: "fix/gripper",
		Base: "main",
	}, o)

	require.Contains(t, body, "fix/gripper")
	require.Contains(t, body, "main")
}

func TestRenderCommentNamesTheAuthor(t *testing.T) {
	body := forge.RenderComment(forge.Comment{
		Author:    "octocat",
		Body:      "reproduced on the bench",
		CreatedAt: time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
	})

	require.Contains(t, body, "octocat")
	require.Contains(t, body, "reproduced on the bench")
}
