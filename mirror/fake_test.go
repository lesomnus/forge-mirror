package mirror_test

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/lesomnus/forge-mirror/forge"
)

// fakeSource is a fixed set of issues. It filters on `since` the way a real
// source does, because the mirror leans on that filtering to decide what is
// worth writing.
type fakeSource struct {
	name     string
	repos    []forge.Repo
	issues   map[string][]forge.Issue
	comments map[int][]forge.Comment
}

func (s *fakeSource) Name() string { return s.name }

func (s *fakeSource) Repos(ctx context.Context) ([]forge.Repo, error) {
	return s.repos, nil
}

func (s *fakeSource) Issues(ctx context.Context, repo forge.Repo, since time.Time) ([]forge.Issue, error) {
	var out []forge.Issue
	for _, i := range s.issues[repo.String()] {
		if !since.IsZero() && !i.UpdatedAt.After(since) {
			continue
		}
		out = append(out, i)
	}
	return out, nil
}

func (s *fakeSource) Comments(ctx context.Context, repo forge.Repo, number int) ([]forge.Comment, error) {
	return s.comments[number], nil
}

// fakeTarget stores what was written and answers lookups the way the real one
// does: by reading the marker back out of the stored body. Nothing is kept on
// the side, so a test that passes here is a test of the same mechanism the
// GitLab target uses.
type fakeTarget struct {
	repos  map[string]bool
	labels map[string][]string

	issues map[int64]*stored
	notes  map[int64][]string

	nextID int64

	// failRepo makes EnsureRepo fail for one repository, so a test can check
	// that the others still get mirrored.
	failRepo string

	Creates int
	Updates int
}

type stored struct {
	kind forge.Kind
	body string
}

func newFakeTarget() *fakeTarget {
	return &fakeTarget{
		repos:  map[string]bool{},
		labels: map[string][]string{},
		issues: map[int64]*stored{},
		notes:  map[int64][]string{},
	}
}

func (t *fakeTarget) Name() string { return "fake" }

func (t *fakeTarget) EnsureRepo(ctx context.Context, repo forge.Repo) (bool, error) {
	if repo.String() == t.failRepo {
		return false, errors.New("refused")
	}
	if t.repos[repo.String()] {
		return false, nil
	}
	t.repos[repo.String()] = true
	return true, nil
}

func (t *fakeTarget) FindByOrigin(ctx context.Context, repo forge.Repo, o forge.Origin) (forge.Ref, bool, error) {
	marker := forge.Marker(o)
	for id, s := range t.issues {
		if strings.Contains(s.body, marker) {
			return forge.Ref{Kind: s.kind, ID: id}, true, nil
		}
	}
	return forge.Ref{}, false, nil
}

func (t *fakeTarget) Create(ctx context.Context, repo forge.Repo, i forge.Issue, o forge.Origin) (forge.Ref, error) {
	t.nextID++
	id := t.nextID
	t.issues[id] = &stored{kind: forge.TargetKind(i), body: forge.Render(i, o)}
	t.Creates++
	return forge.Ref{Kind: forge.TargetKind(i), ID: id}, nil
}

func (t *fakeTarget) Update(ctx context.Context, repo forge.Repo, ref forge.Ref, i forge.Issue, o forge.Origin) error {
	t.issues[ref.ID] = &stored{kind: ref.Kind, body: forge.Render(i, o)}
	t.Updates++
	return nil
}

func (t *fakeTarget) EnsureLabels(ctx context.Context, repo forge.Repo, labels []string) error {
	t.labels[repo.String()] = append(t.labels[repo.String()], labels...)
	return nil
}

func (t *fakeTarget) Comments(ctx context.Context, repo forge.Repo, ref forge.Ref) ([]forge.Comment, error) {
	var cs []forge.Comment
	for _, b := range t.notes[ref.ID] {
		cs = append(cs, forge.Comment{Body: b})
	}
	return cs, nil
}

func (t *fakeTarget) Comment(ctx context.Context, repo forge.Repo, ref forge.Ref, body string) error {
	t.notes[ref.ID] = append(t.notes[ref.ID], body)
	return nil
}
