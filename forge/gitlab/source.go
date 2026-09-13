package gitlab

import (
	"context"
	"time"

	"github.com/lesomnus/forge-mirror/forge"
	"github.com/lesomnus/z"
	gl "gitlab.com/gitlab-org/api/client-go"
)

// Source reads a GitLab group, which is what the restore after an outage reads:
// the mirror runs GitHub→GitLab, and afterwards the work done in GitLab has to
// come back the other way.
type Source struct {
	c     *gl.Client
	group string
}

func NewSource(o Options) (*Source, error) {
	var opts []gl.ClientOptionFunc
	if o.BaseURL != "" {
		opts = append(opts, gl.WithBaseURL(o.BaseURL))
	}

	c, err := gl.NewClient(o.Token, opts...)
	if err != nil {
		return nil, z.Err(err, "client")
	}

	return &Source{c: c, group: o.Group}, nil
}

func (s *Source) Name() string { return "gitlab" }

func (s *Source) path(repo forge.Repo) string { return s.group + "/" + repo.Name }

func (s *Source) Repos(ctx context.Context) ([]forge.Repo, error) {
	var rs []forge.Repo

	var page int64 = 1
	for {
		ps, res, err := s.c.Groups.ListGroupProjects(s.group, &gl.ListGroupProjectsOptions{
			ListOptions: gl.ListOptions{PerPage: 100, Page: page},
		}, gl.WithContext(ctx))
		if err != nil {
			if isNotFound(err) {
				// No group yet means nothing has been mirrored into it, which is
				// nothing to read back rather than a failure.
				return nil, nil
			}
			return nil, z.Err(err, "list group projects")
		}
		for _, p := range ps {
			rs = append(rs, forge.Repo{Owner: s.group, Name: p.Path})
		}
		if res.NextPage == 0 {
			break
		}
		page = res.NextPage
	}

	return rs, nil
}

// Issues returns what changed, which takes two questions rather than one.
//
// `updated_after` answers for the issues and merge requests themselves, and it
// is a real server-side filter. It does not answer for comments: in GitLab a
// note does not move its issue's updated_at — measured, the issue's timestamp
// sits a few hundred milliseconds *before* the note's — and notes have no time
// filter of their own. So an issue whose only change is a new comment is
// invisible to the first question, and a comment is the commonest thing to
// happen to an issue during an outage.
//
// The events listing is what sees those. It reports `commented on` with the
// note's noteable, so the issues behind new comments can be fetched by iid.
//
// GitHub needs none of this: there a comment bumps the issue, which is why the
// forward direction is one query.
func (s *Source) Issues(ctx context.Context, repo forge.Repo, since time.Time) ([]forge.Issue, error) {
	pid := s.path(repo)

	out := map[forge.Ref]forge.Issue{}

	add := func(i forge.Issue) {
		out[forge.Ref{Kind: i.Kind, ID: int64(i.Number)}] = i
	}

	var page int64 = 1
	for {
		opt := &gl.ListProjectIssuesOptions{
			State:       gl.Ptr("all"),
			ListOptions: gl.ListOptions{PerPage: 100, Page: page},
		}
		if !since.IsZero() {
			opt.UpdatedAfter = gl.Ptr(since)
		}
		is, res, err := s.c.Issues.ListProjectIssues(pid, opt, gl.WithContext(ctx))
		if err != nil {
			if isNotFound(err) {
				return nil, nil
			}
			return nil, z.Err(err, "list issues")
		}
		for _, i := range is {
			add(issueOf(i))
		}
		if res.NextPage == 0 {
			break
		}
		page = res.NextPage
	}

	page = 1
	for {
		opt := &gl.ListProjectMergeRequestsOptions{
			State:       gl.Ptr("all"),
			ListOptions: gl.ListOptions{PerPage: 100, Page: page},
		}
		if !since.IsZero() {
			opt.UpdatedAfter = gl.Ptr(since)
		}
		ms, res, err := s.c.MergeRequests.ListProjectMergeRequests(pid, opt, gl.WithContext(ctx))
		if err != nil {
			if isNotFound(err) {
				break
			}
			return nil, z.Err(err, "list merge requests")
		}
		for _, m := range ms {
			add(mergeRequestOf(m))
		}
		if res.NextPage == 0 {
			break
		}
		page = res.NextPage
	}

	// Now the ones only a comment touched.
	commented, err := s.commentedSince(ctx, pid, since)
	if err != nil {
		return nil, err
	}
	for ref := range commented {
		if _, have := out[ref]; have {
			continue
		}
		i, err := s.get(ctx, pid, ref)
		if err != nil {
			if isNotFound(err) {
				// Deleted between the event and now.
				continue
			}
			return nil, err
		}
		add(i)
	}

	is := make([]forge.Issue, 0, len(out))
	for _, i := range out {
		is = append(is, i)
	}
	return is, nil
}

// commentedSince names what got a comment, from the events listing.
//
// `after` there takes a date and nothing finer: a timestamp is truncated to its
// day, and the day given is excluded. So it is asked for the day before `since`
// and over-reads up to two days. That is harmless — a restore runs once, and
// what comes back too much is filtered by the markers anyway — but it is the
// reason this cannot be made precise.
func (s *Source) commentedSince(ctx context.Context, pid string, since time.Time) (map[forge.Ref]struct{}, error) {
	refs := map[forge.Ref]struct{}{}

	opt := &gl.ListProjectVisibleEventsOptions{
		ListOptions: gl.ListOptions{PerPage: 100},
	}
	if !since.IsZero() {
		d := gl.ISOTime(since.AddDate(0, 0, -1))
		opt.After = &d
	}

	var page int64 = 1
	for {
		opt.ListOptions.Page = page
		evs, res, err := s.c.Events.ListProjectVisibleEvents(pid, opt, gl.WithContext(ctx))
		if err != nil {
			if isNotFound(err) {
				return refs, nil
			}
			return nil, z.Err(err, "list events")
		}
		for _, e := range evs {
			if e.TargetType != "Note" {
				continue
			}
			var kind forge.Kind
			switch e.Note.NoteableType {
			case "Issue":
				kind = forge.KindIssue
			case "MergeRequest":
				kind = forge.KindPull
			default:
				continue
			}
			refs[forge.Ref{Kind: kind, ID: int64(e.Note.NoteableIID)}] = struct{}{}
		}
		if res.NextPage == 0 {
			break
		}
		page = res.NextPage
	}

	return refs, nil
}

func (s *Source) get(ctx context.Context, pid string, ref forge.Ref) (forge.Issue, error) {
	if ref.Kind == forge.KindPull {
		m, _, err := s.c.MergeRequests.GetMergeRequest(pid, ref.ID, nil, gl.WithContext(ctx))
		if err != nil {
			return forge.Issue{}, err
		}
		return mergeRequestOf(&m.BasicMergeRequest), nil
	}

	i, _, err := s.c.Issues.GetIssue(pid, ref.ID, gl.WithContext(ctx))
	if err != nil {
		return forge.Issue{}, err
	}
	return issueOf(i), nil
}

func (s *Source) Comments(ctx context.Context, repo forge.Repo, i forge.Issue) ([]forge.Comment, error) {
	pid := s.path(repo)

	var cs []forge.Comment
	var page int64 = 1
	for {
		lo := gl.ListOptions{PerPage: 100, Page: page}

		var ns []*gl.Note
		var res *gl.Response
		var err error
		if i.Kind == forge.KindPull {
			ns, res, err = s.c.Notes.ListMergeRequestNotes(pid, int64(i.Number),
				&gl.ListMergeRequestNotesOptions{ListOptions: lo}, gl.WithContext(ctx))
		} else {
			ns, res, err = s.c.Notes.ListIssueNotes(pid, int64(i.Number),
				&gl.ListIssueNotesOptions{ListOptions: lo}, gl.WithContext(ctx))
		}
		if err != nil {
			if isNotFound(err) {
				return cs, nil
			}
			return nil, z.Err(err, "list notes")
		}

		for _, n := range ns {
			// System notes are GitLab's own bookkeeping — "changed title",
			// "assigned to" — not anybody's comment.
			if n.System {
				continue
			}
			c := forge.Comment{ID: int64(n.ID), Body: n.Body}
			if n.Author.Username != "" {
				c.Author = n.Author.Username
			}
			if n.CreatedAt != nil {
				c.CreatedAt = *n.CreatedAt
			}
			if n.UpdatedAt != nil {
				c.UpdatedAt = *n.UpdatedAt
			}
			cs = append(cs, c)
		}

		if res.NextPage == 0 {
			break
		}
		page = res.NextPage
	}

	return cs, nil
}

func issueOf(i *gl.Issue) forge.Issue {
	o := forge.Issue{
		Number: int(i.IID),
		Kind:   forge.KindIssue,
		Title:  i.Title,
		Body:   i.Description,
		State:  stateOf(i.State),
		URL:    i.WebURL,
		Labels: i.Labels,
	}
	if i.Author != nil {
		o.Author = i.Author.Username
	}
	if i.Milestone != nil {
		o.Milestone = i.Milestone.Title
	}
	for _, a := range i.Assignees {
		if a != nil {
			o.Assignees = append(o.Assignees, a.Username)
		}
	}
	if i.CreatedAt != nil {
		o.CreatedAt = *i.CreatedAt
	}
	if i.UpdatedAt != nil {
		o.UpdatedAt = *i.UpdatedAt
	}
	if i.ClosedAt != nil {
		o.ClosedAt = *i.ClosedAt
	}
	return o
}

func mergeRequestOf(m *gl.BasicMergeRequest) forge.Issue {
	o := forge.Issue{
		Number: int(m.IID),
		Kind:   forge.KindPull,
		Title:  m.Title,
		Body:   m.Description,
		State:  stateOf(m.State),
		URL:    m.WebURL,
		Labels: m.Labels,
		Head:   m.SourceBranch,
		Base:   m.TargetBranch,
	}
	if m.Author != nil {
		o.Author = m.Author.Username
	}
	if m.Milestone != nil {
		o.Milestone = m.Milestone.Title
	}
	for _, a := range m.Assignees {
		if a != nil {
			o.Assignees = append(o.Assignees, a.Username)
		}
	}
	if m.CreatedAt != nil {
		o.CreatedAt = *m.CreatedAt
	}
	if m.UpdatedAt != nil {
		o.UpdatedAt = *m.UpdatedAt
	}
	return o
}

// stateOf maps GitLab's vocabulary onto the two states a forge agrees on.
// GitLab says "opened"/"closed" for issues and adds "merged"/"locked" for merge
// requests; everything that is not open is closed as far as a mirror cares.
func stateOf(s string) forge.State {
	if s == "opened" {
		return forge.StateOpen
	}
	return forge.StateClosed
}
