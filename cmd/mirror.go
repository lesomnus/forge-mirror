package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/lesomnus/forge-mirror/forge"
	gh "github.com/lesomnus/forge-mirror/forge/github"
	gl "github.com/lesomnus/forge-mirror/forge/gitlab"
	"github.com/lesomnus/forge-mirror/mirror"
	"github.com/lesomnus/xli"
	"github.com/lesomnus/xli/flg"
	"github.com/lesomnus/z"
)

func NewCmdMirror() *xli.Command {
	return &xli.Command{
		Name:  "mirror",
		Brief: "copy issues and pull requests from the source forge into the target",

		Flags: flg.Flags{
			&flg.Switch{Name: "dry-run", Brief: "report what would be written without writing it"},
		},

		Handler: xli.OnRun(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
			c := use_config.Must(ctx)
			flg.VisitP(cmd, "dry-run", &c.DryRun)

			src, err := gh.NewSource(gh.Options{
				Token:   c.Source.Token,
				Owner:   c.Source.Owner,
				Repos:   c.Source.Repos,
				BaseURL: c.Source.BaseURL,
			})
			if err != nil {
				return z.Err(err, "source")
			}

			dst, err := gl.NewTarget(gl.Options{
				Token:   c.Target.Token,
				BaseURL: c.Target.BaseURL,
				Group:   c.Target.Group,
			})
			if err != nil {
				return z.Err(err, "target")
			}

			// Where the last clean run got to, capped by how far back the
			// configuration allows this one to look.
			started := time.Now()
			since := sinceFor(started, c.Since, readCursor(c.StatePath))

			m := &mirror.Mirror{
				Source: src,
				Target: dst,
				DryRun: c.DryRun,
				OnError: func(repo forge.Repo, err error) {
					// Printed, not returned: one repository failing must not
					// stop the others. The exit code below still says the run
					// was not clean.
					cmd.Println(fmt.Sprintf("[mirror] FAIL %s: %v", repo, err))
				},
			}

			r, err := m.Run(ctx, since)
			if err != nil {
				return err
			}

			// The cursor moves only when every repository succeeded and only
			// when something was actually written: a run that failed somewhere,
			// or one that was only pretending, must not let the next one skip
			// past what it did not copy.
			//
			// It records a little before this run *started*, not when it
			// finished. Starting time covers anything changed while the run was
			// in flight; the margin covers a source whose answer about what
			// changed is an index that lags behind its own data, which is what
			// GitHub's search is. Re-reading a few minutes costs a few updates.
			if r.Failed == 0 && !c.DryRun {
				if err := writeCursor(c.StatePath, started.Add(-cursorMargin)); err != nil {
					// The run did its work. Failing to save the optimisation
					// only costs the next run some reading.
					cmd.Println(fmt.Sprintf("[mirror] cursor: %v", err))
				}
			}

			if err := observe(ctx, r, time.Since(started)); err != nil {
				// Telemetry failing is not the run failing. The mirror already
				// did its work; refusing to report success because a counter
				// could not be created would be the tail wagging the dog.
				cmd.Println(fmt.Sprintf("[mirror] telemetry: %v", err))
			}

			cmd.Println(fmt.Sprintf(
				"[mirror] repos=%d created=%d updated=%d notes=%d failed=%d",
				r.Repos, r.Created, r.Updated, r.Notes, r.Failed))

			if r.Failed > 0 {
				return fmt.Errorf("%d repositories failed", r.Failed)
			}
			return nil
		}),
	}
}
