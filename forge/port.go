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
	Comments(ctx context.Context, repo Repo, number int) ([]Comment, error)
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

	// FindByOrigin looks up what this program previously created for o, by the
	// marker in the body. A miss is not an error: it means not yet mirrored.
	//
	// The mapping is read back out of the target rather than kept beside it, so
	// that losing or rolling back local state cannot make the program create a
	// second copy of everything.
	FindByOrigin(ctx context.Context, repo Repo, o Origin) (Ref, bool, error)

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
}
