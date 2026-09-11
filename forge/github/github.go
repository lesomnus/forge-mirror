// Package github reads a GitHub organisation as a forge.Source.
package github

import (
	"context"
	"strings"
	"time"

	"github.com/google/go-github/v91/github"
	"github.com/lesomnus/forge-mirror/forge"
	"github.com/lesomnus/z"
)

type Source struct {
	c     *github.Client
	owner string

	// repos limits the scope. Empty means every repository the owner has.
	repos []string
}

type Options struct {
	Token   string
	Owner   string
	Repos   []string
	BaseURL string
}

func NewSource(o Options) (*Source, error) {
	opts := []github.ClientOptionsFunc{github.WithAuthToken(o.Token)}
	if o.BaseURL != "" {
		opts = append(opts, github.WithEnterpriseURLs(o.BaseURL, o.BaseURL))
	}

	c, err := github.NewClient(opts...)
	if err != nil {
		return nil, z.Err(err, "client")
	}

	return &Source{c: c, owner: o.Owner, repos: o.Repos}, nil
}

func (s *Source) Name() string { return "github" }

func (s *Source) Repos(ctx context.Context) ([]forge.Repo, error) {
	// Paging stops on an empty **page**, not on an empty filtered result, so
	// archived repositories come along too. A copy that quietly leaves out
	// history is not the copy this is for.
	opt := &github.RepositoryListByOrgOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}

	var rs []forge.Repo
	for {
		page, res, err := s.c.Repositories.ListByOrg(ctx, s.owner, opt)
		if err != nil {
			return nil, z.Err(err, "list repositories")
		}
		for _, r := range page {
			name := r.GetName()
			if !s.selects(name) {
				continue
			}
			rs = append(rs, forge.Repo{Owner: s.owner, Name: name})
		}
		if res.NextPage == 0 {
			break
		}
		opt.ListOptions.Page = res.NextPage
	}

	return rs, nil
}

func (s *Source) selects(name string) bool {
	if len(s.repos) == 0 {
		return true
	}
	for _, r := range s.repos {
		if r == name {
			return true
		}
	}
	return false
}

func (s *Source) Issues(ctx context.Context, repo forge.Repo, since time.Time) ([]forge.Issue, error) {
	// GitHub returns pull requests from the issues endpoint as well: they share
	// one number space and this is the only listing that spans both. `Since`
	// is what makes a run incremental.
	opt := &github.IssueListByRepoOptions{
		State:       "all",
		Sort:        "updated",
		Direction:   "asc",
		Since:       since,
		ListOptions: github.ListOptions{PerPage: 100},
	}

	var is []forge.Issue
	for {
		page, res, err := s.c.Issues.ListByRepo(ctx, repo.Owner, repo.Name, opt)
		if err != nil {
			return nil, z.Err(err, "list issues")
		}
		for _, gi := range page {
			i := toIssue(gi)

			// The issues listing does not carry the branches, and a merge
			// request cannot be created without them. Only an open pull request
			// becomes one, so only an open pull request is worth the extra
			// round trip.
			if i.Kind == forge.KindPull && i.State == forge.StateOpen {
				if err := s.fillBranches(ctx, repo, &i); err != nil {
					return nil, err
				}
			}

			is = append(is, i)
		}
		if res.NextPage == 0 {
			break
		}
		opt.ListOptions.Page = res.NextPage
	}

	return is, nil
}

func (s *Source) fillBranches(ctx context.Context, repo forge.Repo, i *forge.Issue) error {
	pr, _, err := s.c.PullRequests.Get(ctx, repo.Owner, repo.Name, i.Number)
	if err != nil {
		return z.Err(err, "get pull request")
	}
	i.Head = pr.GetHead().GetRef()
	i.Base = pr.GetBase().GetRef()
	return nil
}

func (s *Source) Comments(ctx context.Context, repo forge.Repo, number int) ([]forge.Comment, error) {
	opt := &github.IssueListCommentsOptions{
		Sort:        github.Ptr("created"),
		Direction:   github.Ptr("asc"),
		ListOptions: github.ListOptions{PerPage: 100},
	}

	var cs []forge.Comment
	for {
		page, res, err := s.c.Issues.ListComments(ctx, repo.Owner, repo.Name, number, opt)
		if err != nil {
			return nil, z.Err(err, "list comments")
		}
		for _, gc := range page {
			cs = append(cs, forge.Comment{
				ID:        gc.GetID(),
				Author:    gc.GetUser().GetLogin(),
				Body:      gc.GetBody(),
				CreatedAt: gc.GetCreatedAt().Time,
				UpdatedAt: gc.GetUpdatedAt().Time,
			})
		}
		if res.NextPage == 0 {
			break
		}
		opt.ListOptions.Page = res.NextPage
	}

	return cs, nil
}

func toIssue(gi *github.Issue) forge.Issue {
	kind := forge.KindIssue
	if gi.IsPullRequest() {
		kind = forge.KindPull
	}

	state := forge.StateOpen
	if strings.EqualFold(gi.GetState(), "closed") {
		state = forge.StateClosed
	}

	labels := make([]string, 0, len(gi.Labels))
	for _, l := range gi.Labels {
		labels = append(labels, l.GetName())
	}

	assignees := make([]string, 0, len(gi.Assignees))
	for _, a := range gi.Assignees {
		assignees = append(assignees, a.GetLogin())
	}

	return forge.Issue{
		Number:    gi.GetNumber(),
		Kind:      kind,
		Title:     gi.GetTitle(),
		Body:      gi.GetBody(),
		State:     state,
		Author:    gi.GetUser().GetLogin(),
		Labels:    labels,
		Milestone: gi.GetMilestone().GetTitle(),
		Assignees: assignees,
		CreatedAt: gi.GetCreatedAt().Time,
		UpdatedAt: gi.GetUpdatedAt().Time,
		ClosedAt:  gi.GetClosedAt().Time,
		URL:       gi.GetHTMLURL(),
	}
}
