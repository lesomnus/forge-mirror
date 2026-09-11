package forge

import (
	"fmt"
	"strconv"
	"strings"
)

// Comments need their own marker.
//
// An issue is found again by its origin; a comment cannot be, because comments
// have no number of their own within an issue — only an id belonging to the
// source. Without a marker the only way to tell which of the source's comments
// are already mirrored is to count them or compare their text, and both are
// wrong the moment somebody edits one or writes a note in the target by hand.
const notePrefix = "forge-mirror-note:"

// NoteMarker renders the comment marker for a source comment id.
func NoteMarker(id int64) string {
	return fmt.Sprintf("<!-- %s%d -->", notePrefix, id)
}

// NoteID reads the source comment id back out of a mirrored comment body. A
// comment written by a person in the target has none, which is how the two are
// told apart.
func NoteID(body string) (int64, bool) {
	i := strings.LastIndex(body, notePrefix)
	if i < 0 {
		return 0, false
	}

	rest := body[i+len(notePrefix):]
	end := strings.IndexAny(rest, " \t\n-")
	if end < 0 {
		end = len(rest)
	}

	n, err := strconv.ParseInt(strings.TrimSpace(rest[:end]), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
