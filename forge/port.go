package forge

import (
	"context"
	"time"
)

// Source is a forge read from.
//
// Both forges implement both ports. That is not symmetry for its own sake: the
// mirror runs GitHub→GitLab, and the restore that follows an outage runs the
// other way, so each side is a Source on one day and a Target on another.
type Source interface {
	// Name is what goes in the marker, e.g. "github". It must be stable: change
	// it and every mapping already written stops being found.
	Name() string

	// Repos lists what to mirror.
	Repos(ctx context.Context) ([]Repo, error)

	// Issues returns issues and pull requests changed at or after since. A zero
	// since means everything, which is the first run and only the first run.
	//
	// Implementations differ in how cheaply they can answer this and it matters
	// a great deal: asking per repository costs one round trip per repository
	// even when nothing changed, while a forge that can answer across a whole
	// organisation in one query collapses that to a handful. Whichever it does,
	// the contract here is the same.
	Issues(ctx context.Context, repo Repo, since time.Time) ([]Issue, error)

	// Comments returns the notes on one issue, oldest first.
	//
	// It takes the issue rather than its number because a number alone does not
	// say what it numbers. GitHub gives issues and pull requests one space, so
	// there a number is enough; GitLab numbers them separately and iid 5 can be
	// both an issue and a merge request.
	Comments(ctx context.Context, repo Repo, i Issue) ([]Comment, error)
}

// Discoverer is an optional capability a [Source] may implement: naming the
// repositories that have something changed since a time, without asking each
// one.
//
// It reports whether its answer can be used. A source that cannot answer for
// the window it was handed — because the window is unbounded, or wider than it
// is able to page through, or because asking failed — says so, and the caller
// lists every repository instead. Answering partially without saying so would
// silently drop repositories from a run, and a mirror that quietly skips things
// is worse than a slow one.
type Discoverer interface {
	ReposChangedSince(ctx context.Context, since time.Time) ([]Repo, bool, error)
}

// Target is a forge written to.
//
// Every method is required to be idempotent. This program is a CronJob that can
// be interrupted at any point and will be run again, so "create it if the last
// run died before it could" has to be the normal path rather than a repair.
type Target interface {
	Name() string

	// EnsureRepo makes sure there is somewhere to put the issues, and reports
	// whether it had to create it.
	EnsureRepo(ctx context.Context, repo Repo) (bool, error)

	// Origins reads back everything this program previously created in one
	// repository, keyed by what it was created from. A repository with nothing
	// mirrored yet gives an empty map, not an error.
	//
	// The mapping is read out of the target rather than kept beside it, so that
	// losing or rolling back local state cannot make the program create a
	// second copy of everything.
	//
	// It is read once for the repository rather than once for each issue in it.
	// One at a time means a search per issue, and forges rate-limit search far
	// harder than listing — GitLab CE allows thirty a minute by default, which
	// a first run over an organisation exhausts in seconds.
	Origins(ctx context.Context, repo Repo) (map[Origin]Ref, error)

	// Create writes a new issue or merge request and returns what it made.
	Create(ctx context.Context, repo Repo, i Issue, o Origin) (Ref, error)

	// Update rewrites one this program created. It must not be used on anything
	// without a marker: that is somebody's own work, not a copy.
	Update(ctx context.Context, repo Repo, ref Ref, i Issue, o Origin) error

	// EnsureLabels creates labels that do not exist yet. Targets reject an
	// unknown label on write, so this runs before Create and Update.
	EnsureLabels(ctx context.Context, repo Repo, labels []string) error

	// Comments returns the comments this program wrote, so a second run can
	// tell which of the source's comments are already there.
	Comments(ctx context.Context, repo Repo, ref Ref) ([]Comment, error)

	// Comment appends one note.
	Comment(ctx context.Context, repo Repo, ref Ref, body string) error

	// SetState moves one to open or closed and touches nothing else.
	//
	// Update will not do for this. The restore reads copies the forward
	// direction rendered, and a copy's body *is* that rendering — decoration,
	// marker and all. Writing it onto the original would wrap the original in a
	// rendering of itself and stamp it as somebody's copy. State is one of the
	// only two things a copy can say about its original without lying; the other
	// is the comments people added to it.
	SetState(ctx context.Context, repo Repo, ref Ref, s State) error
}
