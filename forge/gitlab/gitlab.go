// Package gitlab writes a GitLab group as a forge.Target.
package gitlab

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/lesomnus/forge-mirror/forge"
	"github.com/lesomnus/z"
	gl "gitlab.com/gitlab-org/api/client-go"
)

type Target struct {
	c     *gl.Client
	group string

	groupID int64
}

type Options struct {
	Token   string
	BaseURL string
	Group   string
}

func NewTarget(o Options) (*Target, error) {
	var opts []gl.ClientOptionFunc
	if o.BaseURL != "" {
		opts = append(opts, gl.WithBaseURL(o.BaseURL))
	}

	c, err := gl.NewClient(o.Token, opts...)
	if err != nil {
		return nil, z.Err(err, "client")
	}

	return &Target{c: c, group: o.Group}, nil
}

func (t *Target) Name() string { return "gitlab" }

// EnsureRepo makes the project if it is not there, and reports whether it had
// to. Both the lookup and the create are tolerated failing the way they do when
// somebody else won the race, because two runs overlapping is not an error.
func (t *Target) EnsureRepo(ctx context.Context, repo forge.Repo) (bool, error) {
	if _, err := t.project(ctx, repo); err == nil {
		return false, nil
	} else if !isNotFound(err) {
		return false, err
	}

	gid, err := t.groupIDOf(ctx)
	if err != nil {
		return false, err
	}

	_, _, err = t.c.Projects.CreateProject(&gl.CreateProjectOptions{
		Name:        gl.Ptr(repo.Name),
		Path:        gl.Ptr(repo.Name),
		NamespaceID: gl.Ptr(gid),
		Visibility:  gl.Ptr(gl.PrivateVisibility),
	}, gl.WithContext(ctx))
	if err != nil {
		// Lost a race with another run: it exists now, which is what was asked.
		if _, e := t.project(ctx, repo); e == nil {
			return false, nil
		}
		return false, z.Err(err, "create project")
	}

	return true, nil
}

func (t *Target) groupIDOf(ctx context.Context) (int64, error) {
	if t.groupID != 0 {
		return t.groupID, nil
	}

	g, _, err := t.c.Groups.GetGroup(t.group, nil, gl.WithContext(ctx))
	if err == nil {
		t.groupID = g.ID
		return g.ID, nil
	}
	if !isNotFound(err) {
		return 0, z.Err(err, "get group")
	}

	// The group is the namespace everything mirrored lives in, and on a target
	// that has never been mirrored into there is none. Making it is part of
	// being able to run against an empty forge — which is exactly the state the
	// target is in on the day it is needed, and the state a test starts from.
	g, _, err = t.c.Groups.CreateGroup(&gl.CreateGroupOptions{
		Name:       gl.Ptr(t.group),
		Path:       gl.Ptr(t.group),
		Visibility: gl.Ptr(gl.PrivateVisibility),
	}, gl.WithContext(ctx))
	if err != nil {
		// Lost a race with another run.
		if g2, _, e := t.c.Groups.GetGroup(t.group, nil, gl.WithContext(ctx)); e == nil {
			t.groupID = g2.ID
			return g2.ID, nil
		}
		return 0, z.Err(err, "create group")
	}

	t.groupID = g.ID
	return g.ID, nil
}

func (t *Target) path(repo forge.Repo) string {
	return t.group + "/" + repo.Name
}

func (t *Target) project(ctx context.Context, repo forge.Repo) (*gl.Project, error) {
	p, _, err := t.c.Projects.GetProject(t.path(repo), nil, gl.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	return p, nil
}

// FindByOrigin looks the mapping up out of the target itself.
//
// The marker is matched with the description search, which GitLab answers with
// a single `ILIKE '%…%'` over the column — the query is not split into words,
// so a marker containing spaces and punctuation matches as written. It is an
// unindexed scan, so an index may be kept as a cache; it must stay rebuildable
// from here, or losing it would make the program create a second copy of
// everything.
func (t *Target) FindByOrigin(ctx context.Context, repo forge.Repo, o forge.Origin) (forge.Ref, bool, error) {
	marker := forge.Marker(o)
	pid := t.path(repo)

	is, _, err := t.c.Issues.ListProjectIssues(pid, &gl.ListProjectIssuesOptions{
		Search:      gl.Ptr(marker),
		In:          gl.Ptr("description"),
		ListOptions: gl.ListOptions{PerPage: 2},
	}, gl.WithContext(ctx))
	if err != nil && !isNotFound(err) {
		return forge.Ref{}, false, z.Err(err, "search issues")
	}
	for _, i := range is {
		if strings.Contains(i.Description, marker) {
			return forge.Ref{Kind: forge.KindIssue, ID: i.IID}, true, nil
		}
	}

	// No `in` here: the merge request listing does not take it, so the search
	// spans whatever it spans. The Contains check below is what actually
	// decides, for both listings — the search only narrows what has to be
	// looked at, and a target that widened it would cost a little time, not
	// correctness.
	ms, _, err := t.c.MergeRequests.ListProjectMergeRequests(pid, &gl.ListProjectMergeRequestsOptions{
		Search:      gl.Ptr(marker),
		ListOptions: gl.ListOptions{PerPage: 2},
	}, gl.WithContext(ctx))
	if err != nil && !isNotFound(err) {
		return forge.Ref{}, false, z.Err(err, "search merge requests")
	}
	for _, m := range ms {
		if strings.Contains(m.Description, marker) {
			return forge.Ref{Kind: forge.KindPull, ID: m.IID}, true, nil
		}
	}

	return forge.Ref{}, false, nil
}

func (t *Target) Create(ctx context.Context, repo forge.Repo, i forge.Issue, o forge.Origin) (forge.Ref, error) {
	pid := t.path(repo)
	body := forge.Render(i, o)

	if forge.TargetKind(i) == forge.KindPull {
		m, _, err := t.c.MergeRequests.CreateMergeRequest(pid, &gl.CreateMergeRequestOptions{
			Title:        gl.Ptr(i.Title),
			Description:  gl.Ptr(body),
			SourceBranch: gl.Ptr(i.Head),
			TargetBranch: gl.Ptr(i.Base),
			Labels:       labelsOf(i),
		}, gl.WithContext(ctx))
		if err != nil {
			return forge.Ref{}, z.Err(err, "create merge request")
		}
		return forge.Ref{Kind: forge.KindPull, ID: m.IID}, nil
	}

	n, _, err := t.c.Issues.CreateIssue(pid, &gl.CreateIssueOptions{
		Title:       gl.Ptr(i.Title),
		Description: gl.Ptr(body),
		Labels:      labelsOf(i),
	}, gl.WithContext(ctx))
	if err != nil {
		return forge.Ref{}, z.Err(err, "create issue")
	}

	ref := forge.Ref{Kind: forge.KindIssue, ID: n.IID}
	if i.State == forge.StateClosed {
		if err := t.Update(ctx, repo, ref, i, o); err != nil {
			return ref, err
		}
	}
	return ref, nil
}

func (t *Target) Update(ctx context.Context, repo forge.Repo, ref forge.Ref, i forge.Issue, o forge.Origin) error {
	pid := t.path(repo)
	body := forge.Render(i, o)

	if ref.Kind == forge.KindPull {
		_, _, err := t.c.MergeRequests.UpdateMergeRequest(pid, ref.ID, &gl.UpdateMergeRequestOptions{
			Title:       gl.Ptr(i.Title),
			Description: gl.Ptr(body),
			Labels:      labelsOf(i),
			StateEvent:  stateEvent(i.State, "close", "reopen"),
		}, gl.WithContext(ctx))
		return z.Err(err, "update merge request")
	}

	_, _, err := t.c.Issues.UpdateIssue(pid, ref.ID, &gl.UpdateIssueOptions{
		Title:       gl.Ptr(i.Title),
		Description: gl.Ptr(body),
		Labels:      labelsOf(i),
		StateEvent:  stateEvent(i.State, "close", "reopen"),
	}, gl.WithContext(ctx))
	return z.Err(err, "update issue")
}

// EnsureLabels creates labels that are not there yet.
//
// A label unknown to the project is rejected on write, so this runs first. An
// already-existing label comes back as a conflict, which is the expected result
// on every run after the first and is not an error.
func (t *Target) EnsureLabels(ctx context.Context, repo forge.Repo, labels []string) error {
	pid := t.path(repo)
	for _, name := range labels {
		if name == "" {
			continue
		}
		_, _, err := t.c.Labels.CreateLabel(pid, &gl.CreateLabelOptions{
			Name:  gl.Ptr(name),
			Color: gl.Ptr("#6699cc"),
		}, gl.WithContext(ctx))
		if err != nil && !isConflict(err) {
			return z.Err(err, "create label %q", name)
		}
	}
	return nil
}

func (t *Target) Comments(ctx context.Context, repo forge.Repo, ref forge.Ref) ([]forge.Comment, error) {
	pid := t.path(repo)
	opt := &gl.ListIssueNotesOptions{ListOptions: gl.ListOptions{PerPage: 100}}

	var cs []forge.Comment
	if ref.Kind == forge.KindPull {
		ns, _, err := t.c.Notes.ListMergeRequestNotes(pid, ref.ID,
			&gl.ListMergeRequestNotesOptions{ListOptions: gl.ListOptions{PerPage: 100}},
			gl.WithContext(ctx))
		if err != nil {
			return nil, z.Err(err, "list merge request notes")
		}
		for _, n := range ns {
			cs = append(cs, forge.Comment{ID: int64(n.ID), Body: n.Body})
		}
		return cs, nil
	}

	ns, _, err := t.c.Notes.ListIssueNotes(pid, ref.ID, opt, gl.WithContext(ctx))
	if err != nil {
		return nil, z.Err(err, "list issue notes")
	}
	for _, n := range ns {
		cs = append(cs, forge.Comment{ID: int64(n.ID), Body: n.Body})
	}
	return cs, nil
}

func (t *Target) Comment(ctx context.Context, repo forge.Repo, ref forge.Ref, body string) error {
	pid := t.path(repo)

	if ref.Kind == forge.KindPull {
		_, _, err := t.c.Notes.CreateMergeRequestNote(pid, ref.ID,
			&gl.CreateMergeRequestNoteOptions{Body: gl.Ptr(body)}, gl.WithContext(ctx))
		return z.Err(err, "create merge request note")
	}

	_, _, err := t.c.Notes.CreateIssueNote(pid, ref.ID,
		&gl.CreateIssueNoteOptions{Body: gl.Ptr(body)}, gl.WithContext(ctx))
	return z.Err(err, "create issue note")
}

func labelsOf(i forge.Issue) *gl.LabelOptions {
	if len(i.Labels) == 0 {
		return nil
	}
	l := gl.LabelOptions(i.Labels)
	return &l
}

func stateEvent(s forge.State, closed string, open string) *string {
	if s == forge.StateClosed {
		return gl.Ptr(closed)
	}
	return gl.Ptr(open)
}

// 404 is the one status this client does not report as an *ErrorResponse: it
// returns the sentinel gl.ErrNotFound instead. Matching on the response code
// therefore never sees a 404, and "not there yet" — the normal case on a target
// that has never been mirrored into — comes back as a hard failure.
func isNotFound(err error) bool { return errors.Is(err, gl.ErrNotFound) }

func isConflict(err error) bool { return hasStatus(err, http.StatusConflict) }

func hasStatus(err error, code int) bool {
	var e *gl.ErrorResponse
	if errors.As(err, &e) {
		return e.HasStatusCode(code)
	}
	return false
}
