package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/lesomnus/z"
)

// A cursor is where the last clean run got to.
//
// It is an optimisation, not a record of what was copied — that lives in the
// target and is read back from the markers. So losing it costs one expensive
// run and nothing else, which is what every run did before there was one. That
// is why it may live on a disk that can be wiped, and why a cursor that cannot
// be read is treated as one that is not there.
type cursor struct {
	LastRun time.Time `json:"lastRun"`
}

// readCursor returns where the last clean run got to, or the zero time if there
// is no usable cursor — no path configured, nothing written yet, or something
// written that cannot be read.
func readCursor(p string) time.Time {
	if p == "" {
		return time.Time{}
	}

	b, err := os.ReadFile(p)
	if err != nil {
		return time.Time{}
	}

	var c cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return time.Time{}
	}
	return c.LastRun
}

// writeCursor records where this run got to, atomically: a run interrupted
// mid-write must leave either the old cursor or the new one, never half of
// either. A half-written cursor that parses as a *later* time than it should
// would make the next run skip work, which is the one failure this file must
// not have.
func writeCursor(p string, t time.Time) error {
	if p == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return z.Err(err, "create directory")
	}

	b, err := json.Marshal(cursor{LastRun: t})
	if err != nil {
		return z.Err(err, "encode")
	}

	tmp, err := os.CreateTemp(filepath.Dir(p), ".cursor-*")
	if err != nil {
		return z.Err(err, "create temporary file")
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return z.Err(err, "write")
	}
	if err := tmp.Close(); err != nil {
		return z.Err(err, "close")
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return z.Err(err, "rename")
	}
	return nil
}

// sinceFor is how far back this run looks.
//
// limit is what the configuration allows, zero meaning no limit, and at is
// where the last clean run got to. The limit is the safety rail: it keeps a
// first run, or one after the cursor was lost, from deciding to fetch a decade
// of issues. The cursor moves the window forward when it can, and never past
// the limit — so a job whose runs have been failing for longer than its limit
// still reads only what the limit allows, and the unlimited daily pass is what
// picks up the rest.
func sinceFor(now time.Time, limit time.Duration, at time.Time) time.Time {
	var since time.Time
	if limit > 0 {
		since = now.Add(-limit)
	}
	if at.After(since) {
		since = at
	}
	return since
}
