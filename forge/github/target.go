package github

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/go-github/v91/github"
	"github.com/lesomnus/forge-mirror/forge"
	"github.com/lesomnus/z"
)

// Target writes a GitHub owner, which is what the restore after an outage
// writes: the mirror runs GitHub→GitLab, and afterwards the work done in GitLab
// has to come back.
//
// ── a mirrored pull request becomes an issue here ───────────────────────────
// A pull request is not something a forge will make from a description. It needs
// two branches and a diff between them, and this program carries no git — the
// code comes back separately, through a repository mirror. So a pull request
// read from the source is written here as an issue, the same form a closed one
// already takes on the way out. The marker still records that it was a pull
// request.
type Target struct {
	c     *github.Client
	owner string

	// milestones is what this owner calls the milestones of a repository, by the
	// title the source knows them under, for repositories this run has asked
	// about. GitHub takes a number on write, not a title, so it has to be looked
	// up — once for a repository rather than once for an issue. Run-scoped: a
	// number kept past the run would point at whatever somebody renamed.
	milestones map[string]map[string]int
}

func NewTarget(o Options) (*Target, error) {
	opts := []github.ClientOptionsFunc{github.WithAuthToken(o.Token)}
	if o.BaseURL != "" {
		opts = append(opts, github.WithEnterpriseURLs(o.BaseURL, o.BaseURL))
	}

	c, err := github.NewClient(opts...)
	if err != nil {
		return nil, z.Err(err, "client")
	}

	return &Target{c: c, owner: o.Owner}, nil
}

func (t *Target) Name() string { return "github" }

// EnsureRepo makes the repository if it is not there, and reports whether it
// had to.
//
// Creating one needs more permission than everything else here put together —
// a token that may only read and write issues cannot make a repository — so the
// failure is left to speak for itself rather than being smoothed over. A
// repository that exists in the mirror and not here is the case this covers, and
// it is rare: the repository mirror that carries the code would have made it.
func (t *Target) EnsureRepo(ctx context.Context, repo forge.Repo) (bool, error) {
	if _, _, err := t.c.Repositories.Get(ctx, t.owner, repo.Name); err == nil {
		return false, nil
	} else if !isNotFound(err) {
		return false, z.Err(err, "get repository")
	}

	// An empty owner means the authenticated user's own account; an organisation
	// is named.
	org := t.owner
	if _, _, err := t.c.Organizations.Get(ctx, t.owner); isNotFound(err) {
		org = ""
	}

	_, _, err := t.c.Repositories.Create(ctx, org, &github.Repository{
		Name:    github.Ptr(repo.Name),
		Private: github.Ptr(true),
	})
	if err != nil {
		return false, z.Err(err, "create repository")
	}
	return true, nil
}

// Origins reads back everything this program previously created here.
//
// One listing covers both: GitHub returns pull requests from the issues endpoint
// as well, and gives them one number space, so nothing has to be asked twice and
// a number cannot mean two things.
func (t *Target) Origins(ctx context.Context, repo forge.Repo) (map[forge.Origin]forge.Ref, error) {
	out := map[forge.Origin]forge.Ref{}

	opt := &github.IssueListByRepoOptions{
		State:       "all",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		page, res, err := t.c.Issues.ListByRepo(ctx, t.owner, repo.Name, opt)
		if err != nil {
			if isNotFound(err) {
				// Nothing has been written here yet, which is what the empty map
				// says. That is the state this side is in on the day it is needed.
				return out, nil
			}
			return nil, z.Err(err, "list issues")
		}
		for _, gi := range page {
			o, ok := forge.ParseOrigin(gi.GetBody())
			if !ok {
				continue
			}
			kind := forge.KindIssue
			if gi.IsPullRequest() {
				kind = forge.KindPull
			}
			out[o] = forge.Ref{Kind: kind, ID: int64(gi.GetNumber())}
		}
		if res.NextPage == 0 {
			break
		}
		opt.ListOptions.Page = res.NextPage
	}

	return out, nil
}

func (t *Target) Create(ctx context.Context, repo forge.Repo, i forge.Issue, o forge.Origin) (forge.Ref, error) {
	body := forge.Render(i, o)

	milestone, err := t.milestoneNumber(ctx, repo, i.Milestone)
	if err != nil {
		return forge.Ref{}, err
	}

	gi, _, err := t.c.Issues.Create(ctx, t.owner, repo.Name, github.CreateIssueRequest{
		Title:     i.Title,
		Body:      github.Ptr(body),
		Labels:    i.Labels,
		Milestone: milestone,
	})
	if err != nil {
		return forge.Ref{}, z.Err(err, "create issue")
	}

	ref := forge.Ref{Kind: forge.KindIssue, ID: int64(gi.GetNumber())}

	// Closed on arrival: GitHub will not take a state on create, so it is a
	// second call. Same shape the GitLab target uses.
	if i.State == forge.StateClosed {
		if err := t.SetState(ctx, repo, ref, i.State); err != nil {
			return ref, err
		}
	}
	return ref, nil
}

func (t *Target) Update(ctx context.Context, repo forge.Repo, ref forge.Ref, i forge.Issue, o forge.Origin) error {
	body := forge.Render(i, o)

	milestone, err := t.milestoneNumber(ctx, repo, i.Milestone)
	if err != nil {
		return err
	}

	// Labels: a nil slice leaves what is there alone, a non-nil one replaces it.
	// The source's labels are the whole truth for a copy, so replacing is right —
	// but only when the source has any, or an issue with none would have the
	// target's cleared on every run.
	_, _, err = t.c.Issues.Update(ctx, t.owner, repo.Name, int(ref.ID), github.UpdateIssueRequest{
		Title:     github.Ptr(i.Title),
		Body:      github.Ptr(body),
		Labels:    i.Labels,
		State:     github.Ptr(stateOf(i.State)),
		Milestone: milestone,
	})
	return z.ErrIf(err, "update issue")
}

// SetState implements [forge.Target].
func (t *Target) SetState(ctx context.Context, repo forge.Repo, ref forge.Ref, s forge.State) error {
	_, _, err := t.c.Issues.Update(ctx, t.owner, repo.Name, int(ref.ID), github.UpdateIssueRequest{
		State: github.Ptr(stateOf(s)),
	})
	return z.ErrIf(err, "set issue state")
}

// EnsureLabels creates labels that are not there yet.
//
// An already-existing label comes back as 422, which is the expected result on
// every run after the first and is not an error.
func (t *Target) EnsureLabels(ctx context.Context, repo forge.Repo, labels []string) error {
	for _, name := range labels {
		if name == "" {
			continue
		}
		_, _, err := t.c.Issues.CreateLabel(ctx, t.owner, repo.Name, github.CreateIssueLabelRequest{
			Name:  name,
			Color: github.Ptr("6699cc"),
		})
		if err != nil && !isUnprocessable(err) {
			return z.Err(err, "create label %q", name)
		}
	}
	return nil
}

func (t *Target) Comments(ctx context.Context, repo forge.Repo, ref forge.Ref) ([]forge.Comment, error) {
	var cs []forge.Comment

	opt := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		page, res, err := t.c.Issues.ListComments(ctx, t.owner, repo.Name, int(ref.ID), opt)
		if err != nil {
			if isNotFound(err) {
				return cs, nil
			}
			return nil, z.Err(err, "list comments")
		}
		for _, gc := range page {
			cs = append(cs, forge.Comment{
				ID:     gc.GetID(),
				Author: gc.GetUser().GetLogin(),
				Body:   gc.GetBody(),
			})
		}
		if res.NextPage == 0 {
			break
		}
		opt.ListOptions.Page = res.NextPage
	}

	return cs, nil
}

func (t *Target) Comment(ctx context.Context, repo forge.Repo, ref forge.Ref, body string) error {
	_, _, err := t.c.Issues.CreateComment(ctx, t.owner, repo.Name, int(ref.ID),
		github.IssueCommentRequest{Body: body})
	return z.ErrIf(err, "create comment")
}

// milestoneNumber is what this owner calls the milestone titled `title`, making
// it if it is not there. An empty title, which is most issues, is no milestone
// and no request.
func (t *Target) milestoneNumber(ctx context.Context, repo forge.Repo, title string) (*int, error) {
	if title == "" {
		return nil, nil
	}

	key := repo.String()
	byTitle, ok := t.milestones[key]
	if !ok {
		var err error
		if byTitle, err = t.listMilestones(ctx, repo); err != nil {
			return nil, err
		}
		if t.milestones == nil {
			t.milestones = map[string]map[string]int{}
		}
		t.milestones[key] = byTitle
	}
	if n, ok := byTitle[title]; ok {
		return github.Ptr(n), nil
	}

	ms, _, err := t.c.Issues.CreateMilestone(ctx, t.owner, repo.Name,
		github.CreateMilestoneRequest{Title: title})
	switch {
	case err == nil:
		byTitle[title] = ms.GetNumber()
		return github.Ptr(ms.GetNumber()), nil

	case isUnprocessable(err):
		// Made since the listing was taken. Read it back rather than guess.
		fresh, err := t.listMilestones(ctx, repo)
		if err != nil {
			return nil, err
		}
		t.milestones[key] = fresh
		if n, ok := fresh[title]; ok {
			return github.Ptr(n), nil
		}
		// Refused as existing and then not there. A milestone is not worth
		// failing the issue that carries it.
		return nil, nil

	default:
		return nil, z.Err(err, "create milestone %q", title)
	}
}

func (t *Target) listMilestones(ctx context.Context, repo forge.Repo) (map[string]int, error) {
	byTitle := map[string]int{}

	opt := &github.MilestoneListOptions{
		State:       "all",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		page, res, err := t.c.Issues.ListMilestones(ctx, t.owner, repo.Name, opt)
		if err != nil {
			if isNotFound(err) {
				return byTitle, nil
			}
			return nil, z.Err(err, "list milestones")
		}
		for _, m := range page {
			byTitle[m.GetTitle()] = m.GetNumber()
		}
		if res.NextPage == 0 {
			break
		}
		opt.ListOptions.Page = res.NextPage
	}

	return byTitle, nil
}

// stateOf renders a state the way GitHub spells it.
func stateOf(s forge.State) string {
	if s == forge.StateClosed {
		return "closed"
	}
	return "open"
}

// isUnprocessable reports the 422 GitHub answers when something is already
// there. It is how "the label exists" and "the milestone exists" arrive, and
// both are the ordinary result rather than a failure.
func isUnprocessable(err error) bool {
	var e *github.ErrorResponse
	if errors.As(err, &e) && e.Response != nil {
		return e.Response.StatusCode == http.StatusUnprocessableEntity
	}
	return false
}
