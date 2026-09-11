// Package github reads a GitHub organisation as a forge.Source.
package github

import (
	"context"
	"errors"
	"net/http"
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
	// Named repositories are taken as given. Asking the forge to list an
	// owner's repositories only to throw most of the answer away costs round
	// trips, and it needs a permission that reading two named repositories does
	// not.
	if len(s.repos) > 0 {
		rs := make([]forge.Repo, 0, len(s.repos))
		for _, name := range s.repos {
			rs = append(rs, forge.Repo{Owner: s.owner, Name: name})
		}
		return rs, nil
	}

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
			// An owner can be a person rather than an organisation, and the
			// organisation listing answers 404 for one. The two are told apart
			// by asking, because there is no way to know from the name.
			if isNotFound(err) && opt.Page == 0 {
				return s.reposOfUser(ctx)
			}
			return nil, z.Err(err, "list repositories")
		}
		for _, r := range page {
			rs = append(rs, forge.Repo{Owner: s.owner, Name: r.GetName()})
		}
		if res.NextPage == 0 {
			break
		}
		opt.ListOptions.Page = res.NextPage
	}

	return rs, nil
}

func (s *Source) reposOfUser(ctx context.Context) ([]forge.Repo, error) {
	opt := &github.RepositoryListByUserOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}

	var rs []forge.Repo
	for {
		page, res, err := s.c.Repositories.ListByUser(ctx, s.owner, opt)
		if err != nil {
			return nil, z.Err(err, "list repositories of user")
		}
		for _, r := range page {
			rs = append(rs, forge.Repo{Owner: s.owner, Name: r.GetName()})
		}
		if res.NextPage == 0 {
			break
		}
		opt.ListOptions.Page = res.NextPage
	}

	return rs, nil
}

func isNotFound(err error) bool {
	var e *github.ErrorResponse
	if errors.As(err, &e) && e.Response != nil {
		return e.Response.StatusCode == http.StatusNotFound
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
