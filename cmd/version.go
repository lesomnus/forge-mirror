package cmd

import (
	"context"

	"github.com/lesomnus/forge-mirror/cmd/version"
	"github.com/lesomnus/xli"
)

func NewCmdVersion() *xli.Command {
	const Template = `FORGE_MIRROR_VERSION=%s
FORGE_MIRROR_GIT_REV=%s
FORGE_MIRROR_GIT_DIRTY=%v
`
	return &xli.Command{
		Name:  "version",
		Brief: "print version information",
		Handler: xli.OnRun(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
			v := version.Get()
			cmd.Printf(Template,
				v.Version,
				v.GitRev,
				v.GitDirty,
			)
			return nil
		}),
	}
}
