// Package forge is the domain: what a git forge holds that git itself does not
// carry.
//
// git moves refs and objects. Issues, pull requests, comments, labels and
// milestones are the forge's own invention, so anything that moves them speaks
// a vendor API. That much is unavoidable. What is avoidable is letting the
// vendor's types leak through the program: a `github.Issue` passed around is a
// program that can only ever talk to GitHub.
//
// So the types here are ours. A Source maps its vendor's model onto them, a
// Target maps them onto its own, and everything between the two — deciding what
// changed, what to create, what to leave alone — is written against these and
// can be tested without a network.
package forge

import "time"

// State is whether the thing is still being worked on. Forges agree on this
// much; anything finer (GitHub's "merged", GitLab's "locked") belongs to the
// Source or Target that knows about it.
type State string

const (
	StateOpen   State = "open"
	StateClosed State = "closed"
)

// Kind separates the two things that share a tracker.
//
// GitHub gives issues and pull requests **one number space** — there is exactly
// one #5 — and its /issues endpoint returns both. GitLab gives them two: issue
// !5 and merge request !5 are different objects. That mismatch is the sharpest
// edge in this program, and Kind is where it is named rather than implied.
type Kind string

const (
	KindIssue Kind = "issue"
	KindPull  Kind = "pull"
)

// Repo names a repository within a forge. The forge itself is implied by the
// Source or Target holding it.
type Repo struct {
	Owner string
	Name  string
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }

// Issue is an issue or a pull request. They share a type because GitHub gives
// them one number space and one endpoint; Kind says which it is.
type Issue struct {
	// Number is the number in the *source* forge. It is not necessarily the
	// number the target will give it — GitLab assigns iid on create and will
	// not take one you chose — which is why a mapping is needed at all.
	Number int

	Kind   Kind
	Title  string
	Body   string
	State  State
	Author string

	Labels    []string
	Milestone string
	Assignees []string

	CreatedAt time.Time
	UpdatedAt time.Time
	ClosedAt  time.Time

	// URL is the source's web URL, kept so the mirrored copy can point back at
	// the original while the original is still reachable.
	URL string

	// Head and Base are set for KindPull only. They are branch names, not refs.
	// A merged pull request usually has no Head branch left, which is why a
	// closed pull request is mirrored as an issue rather than a merge request.
	Head string
	Base string
}

// Comment is one note on an Issue.
type Comment struct {
	// ID is the source's identifier. Comments have no per-issue number, so this
	// is what the mapping is keyed on.
	ID     int64
	Author string
	Body   string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Ref points at something this program created in a Target.
type Ref struct {
	// Kind is what the target actually made. It is not always the source's
	// Kind: a closed pull request becomes an issue.
	Kind Kind

	// ID is the target's own number for it — GitLab's iid, for instance.
	//
	// Wider than the source's Number on purpose: this is whatever the target
	// chose, and targets do not agree on the width. Number is the source's and
	// stays as the source gives it.
	ID int64
}
