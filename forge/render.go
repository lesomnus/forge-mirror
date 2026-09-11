package forge

import (
	"fmt"
	"strings"
	"time"
)

// Render builds the body to write into the target.
//
// Three things are stacked: a header saying who wrote the original and when,
// the original body, and the marker.
//
// The header exists because authorship cannot survive the crossing. The people
// who wrote these issues have no account on the target, and even if they did
// this program writes with one token, so every mirrored issue would otherwise
// claim to be written by whoever owns it. Saying so in the body is not as good
// as real authorship, and it is much better than a quiet lie.
func Render(i Issue, o Origin) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("> %s by **%s**", kindNoun(i.Kind), authorOf(i)))
	if !i.CreatedAt.IsZero() {
		b.WriteString(" on " + i.CreatedAt.UTC().Format("2006-01-02"))
	}
	if i.URL != "" {
		b.WriteString(" — [" + o.String() + "](" + i.URL + ")")
	}
	b.WriteString("\n")

	if i.Kind == KindPull && i.Head != "" {
		b.WriteString(fmt.Sprintf("> `%s` → `%s`\n", i.Head, i.Base))
	}

	b.WriteString("\n")
	// The copied text is stripped of markers: the original may quote a mirrored
	// issue, and two markers in one body make the mapping ambiguous.
	if body := stripMarkers(i.Body); body != "" {
		b.WriteString(body)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(Marker(o))
	return b.String()
}

// RenderComment builds the body of one mirrored comment. Same reasoning as
// Render: the author is named because it cannot be represented.
func RenderComment(c Comment) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("> **%s**", commentAuthor(c)))
	if !c.CreatedAt.IsZero() {
		b.WriteString(" on " + c.CreatedAt.UTC().Format("2006-01-02"))
	}
	b.WriteString("\n\n")
	b.WriteString(strings.TrimRight(c.Body, "\n"))
	return b.String()
}

func kindNoun(k Kind) string {
	if k == KindPull {
		return "Pull request"
	}
	return "Issue"
}

func authorOf(i Issue) string {
	if i.Author == "" {
		return "unknown"
	}
	return i.Author
}

func commentAuthor(c Comment) string {
	if c.Author == "" {
		return "unknown"
	}
	return c.Author
}

// TargetKind is what the target should actually create for i.
//
// A pull request is mirrored as a merge request only while it is open, because
// a merge request needs its source branch to exist and a merged pull request
// usually has none — the branch is deleted on merge. Creating one anyway fails,
// and worse, it fails per repository in a job that is otherwise reporting
// success.
//
// A closed pull request is history. It is mirrored as an issue so the
// discussion survives, and the marker records that it was a pull request so a
// restore knows what it was looking at.
func TargetKind(i Issue) Kind {
	if i.Kind == KindPull && i.State == StateOpen {
		return KindPull
	}
	return KindIssue
}

// IsStale reports whether the target copy needs rewriting.
//
// Timestamps come from two different clocks, so they are compared only against
// the copy this program itself recorded for that issue — never source time
// against target time. A zero mirroredAt means nothing has been recorded, which
// is the first-mirror case and is always stale.
func IsStale(i Issue, mirroredAt time.Time) bool {
	if mirroredAt.IsZero() {
		return true
	}
	return i.UpdatedAt.After(mirroredAt)
}
