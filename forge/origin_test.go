package forge_test

import (
	"strings"
	"testing"

	"github.com/lesomnus/forge-mirror/forge"
	"github.com/stretchr/testify/require"
)

func TestMarkerRoundTrip(t *testing.T) {
	tcs := []struct {
		desc string
		o    forge.Origin
	}{
		{
			desc: "issue",
			o:    forge.Origin{Forge: "github", Repo: forge.Repo{Owner: "Holiday-Robot", Name: "holiday"}, Number: 123, Kind: forge.KindIssue},
		},
		{
			desc: "pull request",
			o:    forge.Origin{Forge: "github", Repo: forge.Repo{Owner: "Holiday-Robot", Name: "holiday"}, Number: 7, Kind: forge.KindPull},
		},
		{
			desc: "repository name with dots",
			o:    forge.Origin{Forge: "github", Repo: forge.Repo{Owner: "Holiday-Robot", Name: "wed.hday.dev"}, Number: 147, Kind: forge.KindIssue},
		},
		{
			desc: "repository name with dashes",
			o:    forge.Origin{Forge: "github", Repo: forge.Repo{Owner: "Holiday-Robot", Name: "holiday-robot-docs.deploy"}, Number: 1, Kind: forge.KindIssue},
		},
	}
	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			body := "some text\n\n" + forge.Marker(tc.o)

			got, ok := forge.ParseOrigin(body)
			require.True(t, ok)
			require.Equal(t, tc.o, got)
			require.True(t, forge.HasMarker(body))
		})
	}
}

func TestParseOriginNotMirrored(t *testing.T) {
	// The case a restore depends on telling apart: written by a person in the
	// target while the source was unreachable.
	tcs := []struct {
		desc string
		body string
	}{
		{desc: "empty", body: ""},
		{desc: "plain text", body: "The controller drops frames above 30fps."},
		{desc: "unrelated html comment", body: "text\n<!-- TODO: ask -->"},
		{desc: "prefix mentioned but not a marker", body: "we use forge-mirror-origin: somewhere"},
	}
	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			_, ok := forge.ParseOrigin(tc.body)
			require.False(t, ok)
		})
	}
}

func TestParseOriginTakesLast(t *testing.T) {
	// Render appends this program's marker last, so the last one is ours.
	quoted := forge.Origin{Forge: "github", Repo: forge.Repo{Owner: "o", Name: "r"}, Number: 1, Kind: forge.KindIssue}
	ours := forge.Origin{Forge: "github", Repo: forge.Repo{Owner: "o", Name: "r"}, Number: 2, Kind: forge.KindIssue}

	got, ok := forge.ParseOrigin(forge.Marker(quoted) + "\nquoted above\n" + forge.Marker(ours))
	require.True(t, ok)
	require.Equal(t, ours, got)
}

func TestRenderIsParseable(t *testing.T) {
	// Whatever Render produces must be readable back, or the mirror creates a
	// duplicate on its next run instead of updating what it made.
	o := forge.Origin{Forge: "github", Repo: forge.Repo{Owner: "Holiday-Robot", Name: "holiday"}, Number: 42, Kind: forge.KindIssue}
	i := forge.Issue{
		Number: 42,
		Kind:   forge.KindIssue,
		Title:  "gripper drops payload",
		Body:   "Happens above 2kg.\n\n- [ ] reproduce\n",
		Author: "someone",
		URL:    "https://github.com/Holiday-Robot/holiday/issues/42",
	}

	body := forge.Render(i, o)

	got, ok := forge.ParseOrigin(body)
	require.True(t, ok)
	require.Equal(t, o, got)
	require.Contains(t, body, "Happens above 2kg.")
	require.Contains(t, body, "someone")
}

func TestRenderEmptyBody(t *testing.T) {
	// An issue with a title and no body is ordinary. The marker still has to
	// come out the other side.
	o := forge.Origin{Forge: "github", Repo: forge.Repo{Owner: "o", Name: "r"}, Number: 1, Kind: forge.KindIssue}

	body := forge.Render(forge.Issue{Number: 1, Kind: forge.KindIssue}, o)

	got, ok := forge.ParseOrigin(body)
	require.True(t, ok)
	require.Equal(t, o, got)
}

func TestRenderStripsMarkersFromCopiedBody(t *testing.T) {
	// The original quotes a mirrored issue, marker and all. If that marker
	// survived into the mirrored body there would be two, and the mapping would
	// stop being one unambiguous fact — the mirror could then update a
	// different issue than the one it is mirroring.
	o := forge.Origin{Forge: "github", Repo: forge.Repo{Owner: "o", Name: "r"}, Number: 9, Kind: forge.KindIssue}
	quoted := forge.Origin{Forge: "github", Repo: forge.Repo{Owner: "o", Name: "r"}, Number: 1, Kind: forge.KindIssue}

	body := forge.Render(forge.Issue{
		Number: 9,
		Kind:   forge.KindIssue,
		Body:   "see also:\n" + forge.Marker(quoted) + "\nthat one.",
	}, o)

	got, ok := forge.ParseOrigin(body)
	require.True(t, ok)
	require.Equal(t, o, got)
	require.Contains(t, body, "that one.")
	require.Equal(t, 1, strings.Count(body, "forge-mirror-origin:"), "exactly one marker survives")
}
