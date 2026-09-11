package forge

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Origin says where a mirrored thing came from.
type Origin struct {
	// Forge is the source's name, e.g. "github". It is part of the marker so a
	// target that is fed by two sources one day does not confuse their numbers.
	Forge string
	Repo  Repo

	Kind   Kind
	Number int
}

func (o Origin) String() string {
	return fmt.Sprintf("%s:%s/%s#%d", o.Forge, o.Repo.Owner, o.Repo.Name, o.Number)
}

// The mapping lives in the mirrored body, not in a database beside it.
//
// A table on disk would be faster to read, and it would also be a second thing
// that can be lost, restored to a different point in time, or disagree with the
// target. The marker cannot drift from the issue it is in: delete the issue and
// the mapping goes with it, which is exactly right. An index may still be kept
// as a cache, but it must be rebuildable from the target alone.
//
// It is an HTML comment so that it renders as nothing, and it is searchable
// because targets index the description text.
const markerPrefix = "forge-mirror-origin:"

var markerRe = regexp.MustCompile(
	`<!--\s*` + regexp.QuoteMeta(markerPrefix) + `\s*([a-z0-9_-]+):([^/\s]+)/([^#\s]+)#(\d+)\s+(issue|pull)\s*-->`)

// Marker renders o as the comment embedded in a mirrored body.
func Marker(o Origin) string {
	return fmt.Sprintf("<!-- %s %s:%s/%s#%d %s -->",
		markerPrefix, o.Forge, o.Repo.Owner, o.Repo.Name, o.Number, o.Kind)
}

// ParseOrigin finds the marker this program wrote.
//
// The **last** marker counts, not the first. A body can hold more than one: the
// original may itself quote a mirrored issue, marker and all, and that text is
// copied along with the rest. Render appends this program's marker at the very
// end, so the last one is always ours. Render also strips markers out of the
// copied text, so this should not arise — it is read from the end anyway,
// because getting it wrong means updating a different issue than the one being
// mirrored.
func ParseOrigin(body string) (Origin, bool) {
	ms := markerRe.FindAllStringSubmatch(body, -1)
	if len(ms) == 0 {
		return Origin{}, false
	}
	m := ms[len(ms)-1]

	n, err := strconv.Atoi(m[4])
	if err != nil {
		// Unreachable: the pattern only matches digits. Guarded anyway because
		// a very long run of digits overflows rather than failing to match.
		return Origin{}, false
	}

	return Origin{
		Forge:  m[1],
		Repo:   Repo{Owner: m[2], Name: m[3]},
		Number: n,
		Kind:   Kind(m[5]),
	}, true
}

// stripMarkers removes every marker from text copied out of a source.
//
// Without this a mirrored body can end up with two markers — the one quoted in
// the original and this program's own — and the mapping stops being a single
// unambiguous fact about the issue.
func stripMarkers(s string) string {
	return strings.TrimRight(markerRe.ReplaceAllString(s, ""), " \t\n")
}

// HasMarker reports whether body was written by this program.
//
// This is what separates the two cases a restore has to tell apart: something
// mirrored from the source, and something a person created directly in the
// target while the source was unreachable. The second has no marker and must
// not be treated as a copy of anything.
func HasMarker(body string) bool {
	return strings.Contains(body, markerPrefix)
}
