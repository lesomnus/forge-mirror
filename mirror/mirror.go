// Package mirror carries a Source's issues into a Target.
//
// Everything here is written against forge.Source and forge.Target, so the
// decisions — what to create, what to rewrite, what to leave alone — can be
// exercised without either forge being present. That matters more than usual:
// this runs unattended against a copy nobody reads, and the only signal it
// gives is an exit code.
package mirror

import (
	"context"
	"time"

	"github.com/lesomnus/forge-mirror/forge"
	"github.com/lesomnus/z"
)

type Result struct {
	Repos   int
	Created int
	Updated int
	Notes   int
	Failed  int
}

type Mirror struct {
	Source forge.Source
	Target forge.Target

	// DryRun reports what would be written without writing it.
	DryRun bool

	// OnError is called for each repository that fails. One repository failing
	// must not stop the others: this is a backup, and 273 of 274 is worth far
	// more than nothing. The run still ends non-zero.
	OnError func(repo forge.Repo, err error)
}

func (m *Mirror) Run(ctx context.Context, since time.Time) (Result, error) {
	var r Result

	repos, err := m.repos(ctx, since)
	if err != nil {
		return r, err
	}

	for _, repo := range repos {
		r.Repos++
		if err := m.repo(ctx, repo, since, &r); err != nil {
			r.Failed++
			if m.OnError != nil {
				m.OnError(repo, err)
			}
		}
	}

	return r, nil
}

// repos is what this run will look at.
//
// A source that can say which repositories changed is asked first. For an
// organisation of any size that is a couple of requests rather than one for
// every repository, and it is one for every repository that almost every run
// spends almost all of itself on: nothing has changed in nearly all of them.
//
// It is only ever an optimisation. A source that has no answer, or one it says
// cannot be used, sends this back to listing everything — which is what always
// happened and is always correct.
func (m *Mirror) repos(ctx context.Context, since time.Time) ([]forge.Repo, error) {
	if d, ok := m.Source.(forge.Discoverer); ok {
		rs, usable, err := d.ReposChangedSince(ctx, since)
		if err != nil {
			return nil, z.Err(err, "discover repositories")
		}
		if usable {
			return rs, nil
		}
	}

	rs, err := m.Source.Repos(ctx)
	if err != nil {
		return nil, z.Err(err, "list repositories")
	}
	return rs, nil
}

func (m *Mirror) repo(ctx context.Context, repo forge.Repo, since time.Time, r *Result) error {
	issues, err := m.Source.Issues(ctx, repo, since)
	if err != nil {
		return z.Err(err, "list issues")
	}
	if len(issues) == 0 {
		// Nothing changed. Do not touch the target at all — not even to make
		// sure the project is there. A run that writes nothing should cost
		// nothing, because almost every run is this one.
		return nil
	}

	if m.DryRun {
		r.Created += len(issues)
		return nil
	}

	if _, err := m.Target.EnsureRepo(ctx, repo); err != nil {
		return z.Err(err, "ensure repository")
	}

	// What is already mirrored, read once. Asking per issue costs a search
	// each, and that is the one thing forges meter tightly.
	origins, err := m.Target.Origins(ctx, repo)
	if err != nil {
		return z.Err(err, "read origins")
	}

	// Labels first: a target rejects a label it has never heard of, and it
	// rejects it on the write that carries the issue, so the issue is lost with
	// it. Collected across the batch so this is one pass rather than one per
	// issue.
	if err := m.Target.EnsureLabels(ctx, repo, labelsOf(issues)); err != nil {
		return z.Err(err, "ensure labels")
	}

	for _, i := range issues {
		if err := m.issue(ctx, repo, i, origins, r); err != nil {
			return z.Err(err, "issue %d", i.Number)
		}
	}

	return nil
}

func (m *Mirror) issue(ctx context.Context, repo forge.Repo, i forge.Issue, origins map[forge.Origin]forge.Ref, r *Result) error {
	o := forge.Origin{
		Forge:  m.Source.Name(),
		Repo:   repo,
		Kind:   i.Kind,
		Number: i.Number,
	}

	ref, found := origins[o]

	var err error
	if !found {
		ref, err = m.Target.Create(ctx, repo, i, o)
		if err != nil {
			return z.Err(err, "create")
		}
		r.Created++
	} else {
		// Everything the source returned came back because it changed — that is
		// what `since` asked for — so there is nothing to re-check here.
		if err := m.Target.Update(ctx, repo, ref, i, o); err != nil {
			return z.Err(err, "update")
		}
		r.Updated++
	}

	n, err := m.comments(ctx, repo, i, ref)
	if err != nil {
		return err
	}
	r.Notes += n

	return nil
}

// comments appends the source's comments that are not in the target yet.
//
// Which ones those are is decided by the marker each mirrored comment carries,
// not by counting or by comparing text: counting breaks the moment somebody
// writes a note in the target by hand, and comparing text breaks when somebody
// edits one.
func (m *Mirror) comments(ctx context.Context, repo forge.Repo, i forge.Issue, ref forge.Ref) (int, error) {
	src, err := m.Source.Comments(ctx, repo, i.Number)
	if err != nil {
		return 0, z.Err(err, "list source comments")
	}
	if len(src) == 0 {
		return 0, nil
	}

	dst, err := m.Target.Comments(ctx, repo, ref)
	if err != nil {
		return 0, z.Err(err, "list target comments")
	}

	have := make(map[int64]struct{}, len(dst))
	for _, c := range dst {
		if id, ok := forge.NoteID(c.Body); ok {
			have[id] = struct{}{}
		}
	}

	n := 0
	for _, c := range src {
		if _, ok := have[c.ID]; ok {
			continue
		}
		body := forge.RenderComment(c) + "\n\n" + forge.NoteMarker(c.ID)
		if err := m.Target.Comment(ctx, repo, ref, body); err != nil {
			return n, z.Err(err, "comment")
		}
		n++
	}

	return n, nil
}

func labelsOf(issues []forge.Issue) []string {
	seen := map[string]struct{}{}
	var ls []string
	for _, i := range issues {
		for _, l := range i.Labels {
			if _, ok := seen[l]; ok {
				continue
			}
			seen[l] = struct{}{}
			ls = append(ls, l)
		}
	}
	return ls
}
