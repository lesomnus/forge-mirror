// Package gitlab writes a GitLab group as a forge.Target.
package gitlab

import (
	"context"
	"errors"
	"net/http"

	"github.com/lesomnus/forge-mirror/forge"
	"github.com/lesomnus/z"
	gl "gitlab.com/gitlab-org/api/client-go"
)

type Target struct {
	c     *gl.Client
	group string

	groupID int64

	// milestones is what the target calls the milestones of a repository, by
	// the title the source knows them under, for repositories this run has
	// already asked about.
	//
	// A milestone cannot be named on a write the way a label can — GitLab takes
	// an id — so it has to be looked up, and looking it up once for a repository
	// beats once for every issue in it. Run-scoped: the program is a job that
	// exits, and a stale id would point at a milestone somebody renamed.
	milestones map[string]map[string]int64
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

// hasBranches reports whether both branches are in the target already.
//
// Asked before creating a merge request rather than inferred from the failure:
// the failure is a 400 whose only distinguishing mark is English prose in a
// nested JSON body, and reading that would break the day GitLab rewords it.
func (t *Target) hasBranches(ctx context.Context, pid string, names ...string) bool {
	for _, name := range names {
		if name == "" {
			return false
		}
		if _, _, err := t.c.Branches.GetBranch(pid, name, gl.WithContext(ctx)); err != nil {
			return false
		}
	}
	return true
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
func (t *Target) Origins(ctx context.Context, repo forge.Repo) (map[forge.Origin]forge.Ref, error) {
	pid := t.path(repo)
	out := map[forge.Origin]forge.Ref{}

	// Both listings are walked to the end. The marker lives in the body and no
	// forge offers a server-side "has one", so every page has to be looked at
	// either way — but this is one pass over the repository instead of a search
	// for every issue in it.
	//
	// A project that is not there yet is not an error: it means nothing has
	// been mirrored into it, which is what the empty map says. That is the
	// state the target is in on the day it is needed.
	var page int64 = 1
	for {
		is, res, err := t.c.Issues.ListProjectIssues(pid, &gl.ListProjectIssuesOptions{
			State:       gl.Ptr("all"),
			ListOptions: gl.ListOptions{PerPage: 100, Page: page},
		}, gl.WithContext(ctx))
		if err != nil {
			if isNotFound(err) {
				return out, nil
			}
			return nil, z.Err(err, "list issues")
		}
		for _, i := range is {
			if o, ok := forge.ParseOrigin(i.Description); ok {
				out[o] = forge.Ref{Kind: forge.KindIssue, ID: i.IID}
			}
		}
		if res.NextPage == 0 {
			break
		}
		page = res.NextPage
	}

	page = 1
	for {
		ms, res, err := t.c.MergeRequests.ListProjectMergeRequests(pid, &gl.ListProjectMergeRequestsOptions{
			State:       gl.Ptr("all"),
			ListOptions: gl.ListOptions{PerPage: 100, Page: page},
		}, gl.WithContext(ctx))
		if err != nil {
			if isNotFound(err) {
				return out, nil
			}
			return nil, z.Err(err, "list merge requests")
		}
		for _, m := range ms {
			o, ok := forge.ParseOrigin(m.Description)
			if !ok {
				continue
			}
			// An issue already claimed for this origin wins. One origin should
			// only ever have produced one of the two, but if both are somehow
			// there, preferring the issue keeps the older behaviour: the search
			// this replaced looked at issues first and returned on the first
			// hit.
			if _, taken := out[o]; !taken {
				out[o] = forge.Ref{Kind: forge.KindPull, ID: m.IID}
			}
		}
		if res.NextPage == 0 {
			break
		}
		page = res.NextPage
	}

	return out, nil
}

func (t *Target) Create(ctx context.Context, repo forge.Repo, i forge.Issue, o forge.Origin) (forge.Ref, error) {
	pid := t.path(repo)
	body := forge.Render(i, o)

	milestone, err := t.milestoneID(ctx, repo, i.Milestone)
	if err != nil {
		return forge.Ref{}, err
	}

	// An open pull request becomes a merge request when the target can hold one,
	// and an issue when it cannot. Two things stop it, and both are ordinary
	// rather than exceptional:
	//
	//   the branches are not here — this program does not carry git, a
	//     repository mirror does that separately, and this one may run first or
	//     against a repository nobody mirrors the code of;
	//
	//   the branch already has an open merge request — GitLab allows one per
	//     source branch, while GitHub allows several pull requests from one
	//     branch as long as their bases differ, so a source can hold more of
	//     them than a target can take.
	//
	// Either way it is mirrored as an issue, the same form a closed pull request
	// takes. The marker still records that it was a pull request, so what is
	// lost is the link between the two branches — and an issue holding the
	// discussion beats failing the whole repository, which is what the second
	// case did until it was seen in a first run over a real organisation.
	if forge.TargetKind(i) == forge.KindPull && t.hasBranches(ctx, pid, i.Head, i.Base) {
		m, _, err := t.c.MergeRequests.CreateMergeRequest(pid, &gl.CreateMergeRequestOptions{
			Title:        gl.Ptr(i.Title),
			Description:  gl.Ptr(body),
			SourceBranch: gl.Ptr(i.Head),
			TargetBranch: gl.Ptr(i.Base),
			Labels:       labelsOf(i),
			MilestoneID:  milestone,
		}, gl.WithContext(ctx))
		switch {
		case err == nil:
			return forge.Ref{Kind: forge.KindPull, ID: m.IID}, nil
		case isConflict(err):
			// Fall through to the issue below.
		default:
			return forge.Ref{}, z.Err(err, "create merge request")
		}
	}

	n, _, err := t.c.Issues.CreateIssue(pid, &gl.CreateIssueOptions{
		Title:       gl.Ptr(i.Title),
		Description: gl.Ptr(body),
		Labels:      labelsOf(i),
		MilestoneID: milestone,
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

// Returning a wrap of a possibly-nil error needs `z.ErrIf`, not `z.Err`:
// `z.Err` wraps unconditionally, so on success it hands back a non-nil error
// reading `update issue: %!w(<nil>)` and a run that did its work reports
// failure. Guarded call sites can keep using `z.Err`, since they have already
// established the error is real.
func (t *Target) Update(ctx context.Context, repo forge.Repo, ref forge.Ref, i forge.Issue, o forge.Origin) error {
	pid := t.path(repo)
	body := forge.Render(i, o)

	milestone, err := t.milestoneID(ctx, repo, i.Milestone)
	if err != nil {
		return err
	}

	if ref.Kind == forge.KindPull {
		_, _, err := t.c.MergeRequests.UpdateMergeRequest(pid, ref.ID, &gl.UpdateMergeRequestOptions{
			Title:       gl.Ptr(i.Title),
			Description: gl.Ptr(body),
			Labels:      labelsOf(i),
			MilestoneID: milestone,
			StateEvent:  stateEvent(i.State, "close", "reopen"),
		}, gl.WithContext(ctx))
		return z.ErrIf(err, "update merge request")
	}

	_, _, err = t.c.Issues.UpdateIssue(pid, ref.ID, &gl.UpdateIssueOptions{
		Title:       gl.Ptr(i.Title),
		Description: gl.Ptr(body),
		Labels:      labelsOf(i),
		MilestoneID: milestone,
		StateEvent:  stateEvent(i.State, "close", "reopen"),
	}, gl.WithContext(ctx))
	return z.ErrIf(err, "update issue")
}

// milestoneID is what the target calls the milestone titled `title`, making it
// if it is not there. An empty title, which is most issues, is no milestone and
// no request.
func (t *Target) milestoneID(ctx context.Context, repo forge.Repo, title string) (*int64, error) {
	if title == "" {
		return nil, nil
	}

	pid := t.path(repo)
	byTitle, ok := t.milestones[pid]
	if !ok {
		var err error
		if byTitle, err = t.listMilestones(ctx, pid); err != nil {
			return nil, err
		}
		if t.milestones == nil {
			t.milestones = map[string]map[string]int64{}
		}
		t.milestones[pid] = byTitle
	}
	if id, ok := byTitle[title]; ok {
		return gl.Ptr(id), nil
	}

	m, _, err := t.c.Milestones.CreateMilestone(pid, &gl.CreateMilestoneOptions{
		Title: gl.Ptr(title),
	}, gl.WithContext(ctx))
	switch {
	case err == nil:
		byTitle[title] = m.ID
		return gl.Ptr(m.ID), nil

	case isConflict(err):
		// Made since the listing was taken — by an earlier run, or by a person.
		// Read it back rather than guess at what it was called.
		fresh, err := t.listMilestones(ctx, pid)
		if err != nil {
			return nil, err
		}
		t.milestones[pid] = fresh
		if id, ok := fresh[title]; ok {
			return gl.Ptr(id), nil
		}
		// It answered conflict and then was not there. Nothing sensible is left
		// to point at, and a milestone is not worth failing the issue over.
		return nil, nil

	default:
		return nil, z.Err(err, "create milestone %q", title)
	}
}

func (t *Target) listMilestones(ctx context.Context, pid string) (map[string]int64, error) {
	byTitle := map[string]int64{}

	var page int64 = 1
	for {
		ms, res, err := t.c.Milestones.ListMilestones(pid, &gl.ListMilestonesOptions{
			ListOptions: gl.ListOptions{PerPage: 100, Page: page},
		}, gl.WithContext(ctx))
		if err != nil {
			if isNotFound(err) {
				// Nothing mirrored into this project yet.
				return byTitle, nil
			}
			return nil, z.Err(err, "list milestones")
		}
		for _, m := range ms {
			byTitle[m.Title] = m.ID
		}
		if res.NextPage == 0 {
			break
		}
		page = res.NextPage
	}

	return byTitle, nil
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
		return z.ErrIf(err, "create merge request note")
	}

	_, _, err := t.c.Notes.CreateIssueNote(pid, ref.ID,
		&gl.CreateIssueNoteOptions{Body: gl.Ptr(body)}, gl.WithContext(ctx))
	return z.ErrIf(err, "create issue note")
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
