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

			// `since` bounds the first run rather than being the cursor. The
			// cursor is what the last successful run recorded; until that is
			// persisted this is what keeps a first run from deciding to fetch a
			// decade of issues.
			var since time.Time
			if c.Since > 0 {
				since = time.Now().Add(-c.Since)
			}

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
