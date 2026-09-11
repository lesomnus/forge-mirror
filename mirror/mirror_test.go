package mirror_test

import (
	"context"
	"testing"
	"time"

	"github.com/lesomnus/forge-mirror/forge"
	"github.com/lesomnus/forge-mirror/mirror"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)

func repo() forge.Repo { return forge.Repo{Owner: "acme", Name: "widget"} }

func source(issues ...forge.Issue) *fakeSource {
	return &fakeSource{
		name:     "github",
		repos:    []forge.Repo{repo()},
		issues:   map[string][]forge.Issue{repo().String(): issues},
		comments: map[int][]forge.Comment{},
	}
}

func newMirror(s *fakeSource, t *fakeTarget) *mirror.Mirror {
	return &mirror.Mirror{Source: s, Target: t}
}

// The assertion the whole program rests on. This is a CronJob: it runs again
// every hour whether or not anything changed, and if a second run over
// unchanged input creates anything, the target fills with duplicates unattended
// and nobody is watching.
func TestSecondRunCreatesNothing(t *testing.T) {
	s := source(
		forge.Issue{Number: 1, Kind: forge.KindIssue, Title: "a", State: forge.StateOpen, UpdatedAt: t0},
		forge.Issue{Number: 2, Kind: forge.KindIssue, Title: "b", State: forge.StateClosed, UpdatedAt: t0},
		forge.Issue{Number: 3, Kind: forge.KindPull, Title: "c", State: forge.StateOpen, Head: "f", Base: "main", UpdatedAt: t0},
	)
	tgt := newFakeTarget()
	m := newMirror(s, tgt)

	first, err := m.Run(context.Background(), time.Time{})
	require.NoError(t, err)
	require.Equal(t, 3, first.Created)
	require.Equal(t, 0, first.Updated)

	second, err := m.Run(context.Background(), time.Time{})
	require.NoError(t, err)
	require.Equal(t, 0, second.Created, "a second run over unchanged input must create nothing")
	require.Equal(t, 3, second.Updated)
	require.Equal(t, 3, tgt.Creates, "the target was written to only on the first run")
}

func TestKindDependsOnState(t *testing.T) {
	s := source(
		forge.Issue{Number: 1, Kind: forge.KindPull, Title: "open", State: forge.StateOpen, Head: "f", Base: "main", UpdatedAt: t0},
		forge.Issue{Number: 2, Kind: forge.KindPull, Title: "merged", State: forge.StateClosed, Head: "g", Base: "main", UpdatedAt: t0},
	)
	tgt := newFakeTarget()

	_, err := newMirror(s, tgt).Run(context.Background(), time.Time{})
	require.NoError(t, err)

	kinds := map[forge.Kind]int{}
	for _, st := range tgt.issues {
		kinds[st.kind]++
	}
	require.Equal(t, 1, kinds[forge.KindPull], "the open pull request becomes a merge request")
	require.Equal(t, 1, kinds[forge.KindIssue], "the closed one becomes an issue: its branch is gone")
}

func TestUnchangedRepositoryIsNotTouched(t *testing.T) {
	// Almost every run is this one. It must not cost a write, nor even the
	// call that makes sure the project exists.
	s := source(forge.Issue{Number: 1, Kind: forge.KindIssue, UpdatedAt: t0})
	tgt := newFakeTarget()

	r, err := newMirror(s, tgt).Run(context.Background(), t0.Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, 1, r.Repos)
	require.Equal(t, 0, r.Created)
	require.Empty(t, tgt.repos, "an unchanged repository is not even looked up in the target")
}

func TestCommentsAreNotDuplicated(t *testing.T) {
	s := source(forge.Issue{Number: 1, Kind: forge.KindIssue, UpdatedAt: t0})
	s.comments[1] = []forge.Comment{
		{ID: 100, Author: "a", Body: "first"},
		{ID: 200, Author: "b", Body: "second"},
	}
	tgt := newFakeTarget()
	m := newMirror(s, tgt)

	first, err := m.Run(context.Background(), time.Time{})
	require.NoError(t, err)
	require.Equal(t, 2, first.Notes)

	second, err := m.Run(context.Background(), time.Time{})
	require.NoError(t, err)
	require.Equal(t, 0, second.Notes, "comments already mirrored are recognised by their marker")

	require.Len(t, tgt.notes[1], 2)
}

func TestNewCommentOnAlreadyMirroredIssue(t *testing.T) {
	s := source(forge.Issue{Number: 1, Kind: forge.KindIssue, UpdatedAt: t0})
	s.comments[1] = []forge.Comment{{ID: 100, Body: "first"}}
	tgt := newFakeTarget()
	m := newMirror(s, tgt)

	_, err := m.Run(context.Background(), time.Time{})
	require.NoError(t, err)

	s.comments[1] = append(s.comments[1], forge.Comment{ID: 200, Body: "later"})

	r, err := m.Run(context.Background(), time.Time{})
	require.NoError(t, err)
	require.Equal(t, 1, r.Notes, "only the new one is written")
	require.Len(t, tgt.notes[1], 2)
}

func TestHandWrittenNoteInTargetIsLeftAlone(t *testing.T) {
	// Someone wrote a note in the target while the source was unreachable. It
	// has no marker, and it must neither be counted as a mirrored comment nor
	// removed.
	s := source(forge.Issue{Number: 1, Kind: forge.KindIssue, UpdatedAt: t0})
	s.comments[1] = []forge.Comment{{ID: 100, Body: "from the source"}}
	tgt := newFakeTarget()
	m := newMirror(s, tgt)

	_, err := m.Run(context.Background(), time.Time{})
	require.NoError(t, err)

	tgt.notes[1] = append(tgt.notes[1], "written here by a person during the outage")

	r, err := m.Run(context.Background(), time.Time{})
	require.NoError(t, err)
	require.Equal(t, 0, r.Notes)
	require.Len(t, tgt.notes[1], 2, "the hand-written note survives untouched")
}

func TestOneRepositoryFailingDoesNotStopTheRest(t *testing.T) {
	s := &fakeSource{
		name: "github",
		repos: []forge.Repo{
			{Owner: "acme", Name: "bad"},
			{Owner: "acme", Name: "good"},
		},
		issues: map[string][]forge.Issue{
			"acme/bad":  {{Number: 9, Kind: forge.KindIssue, UpdatedAt: t0}},
			"acme/good": {{Number: 1, Kind: forge.KindIssue, UpdatedAt: t0}},
		},
		comments: map[int][]forge.Comment{},
	}
	tgt := newFakeTarget()
	tgt.failRepo = "acme/bad"

	var failed []forge.Repo
	m := newMirror(s, tgt)
	m.OnError = func(r forge.Repo, err error) { failed = append(failed, r) }

	res, err := m.Run(context.Background(), time.Time{})
	require.NoError(t, err)
	require.Equal(t, 1, res.Failed)
	require.Equal(t, 1, res.Created, "the healthy repository was still mirrored")
	require.Len(t, failed, 1)
}

func TestDryRunWritesNothing(t *testing.T) {
	s := source(forge.Issue{Number: 1, Kind: forge.KindIssue, UpdatedAt: t0})
	tgt := newFakeTarget()

	m := newMirror(s, tgt)
	m.DryRun = true

	_, err := m.Run(context.Background(), time.Time{})
	require.NoError(t, err)
	require.Equal(t, 0, tgt.Creates)
	require.Empty(t, tgt.repos)
}
