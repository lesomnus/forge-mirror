package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSinceFor(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	hoursAgo := func(n int) time.Time { return now.Add(-time.Duration(n) * time.Hour) }

	tcs := []struct {
		desc  string
		limit time.Duration
		at    time.Time
		want  time.Time
	}{
		{
			// The first run of a capped job. The cap is the whole answer.
			desc:  "no cursor, limited",
			limit: 3 * time.Hour,
			want:  hoursAgo(3),
		},
		{
			// The first run of the uncapped job: everything there has ever been.
			desc: "no cursor, unlimited",
			want: time.Time{},
		},
		{
			// The ordinary case. The cursor is newer than the cap allows, so the
			// run reads from the cursor and not the whole window.
			desc:  "cursor newer than the limit",
			limit: 3 * time.Hour,
			at:    hoursAgo(1),
			want:  hoursAgo(1),
		},
		{
			// Runs have been failing for longer than the cap. The cap still
			// bounds the work; the uncapped daily pass is what picks up the rest.
			desc:  "cursor older than the limit",
			limit: 3 * time.Hour,
			at:    hoursAgo(9),
			want:  hoursAgo(3),
		},
		{
			// No cap, so the cursor is taken however old it is.
			desc: "cursor, unlimited",
			at:   hoursAgo(30),
			want: hoursAgo(30),
		},
	}
	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			require.Equal(t, tc.want, sinceFor(now, tc.limit, tc.at))
		})
	}
}

func TestCursorRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "cursor.json")
	at := time.Date(2026, 9, 12, 11, 47, 0, 0, time.UTC)

	require.True(t, readCursor(p).IsZero(), "nothing written yet")
	require.NoError(t, writeCursor(p, at))
	require.True(t, readCursor(p).Equal(at))
}

func TestCursorIsOptional(t *testing.T) {
	t.Run("no path", func(t *testing.T) {
		require.True(t, readCursor("").IsZero())
		require.NoError(t, writeCursor("", time.Now()))
	})
	t.Run("unreadable content is no cursor", func(t *testing.T) {
		// Refusing to run because a cache is corrupt would be worse than
		// reading more than necessary, which is all a missing cursor costs.
		p := filepath.Join(t.TempDir(), "cursor.json")
		require.NoError(t, os.WriteFile(p, []byte("{not json"), 0o644))
		require.True(t, readCursor(p).IsZero())
	})
}
