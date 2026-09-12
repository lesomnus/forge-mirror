package config

import "time"

// These sit at the top level of the configuration rather than under a `mirror:`
// key. The program is the mirror; a section inside it saying so again buys
// nothing and costs a doubled name everywhere the environment is involved —
// `FORGE_MIRROR_MIRROR_SOURCE_TOKEN` rather than `FORGE_MIRROR_SOURCE_TOKEN`.

type SourceConfig struct {
	Token string `yaml:"token"`

	// Owner is the user or organisation to read from.
	Owner string `yaml:"owner"`

	// Repos limits which repositories are mirrored. Empty means every
	// repository the owner has, which is the production case; naming them is
	// how a test, or a first careful run, keeps the blast radius small.
	Repos []string `yaml:"repos"`

	// BaseURL points at a self-hosted instance. Empty means the public one.
	BaseURL string `yaml:"baseUrl"`
}

type TargetConfig struct {
	Token   string `yaml:"token"`
	BaseURL string `yaml:"baseUrl"`

	// Group is the namespace mirrored projects live in. Defaults to the
	// source's owner.
	Group string `yaml:"group"`
}

// Selects reports whether the named repository is in scope.
func (c SourceConfig) Selects(name string) bool {
	if len(c.Repos) == 0 {
		return true
	}
	for _, r := range c.Repos {
		if r == name {
			return true
		}
	}
	return false
}

// Since is the furthest back a run is allowed to look.
//
// It is a safety rail rather than the position: the position is the cursor the
// last clean run recorded (see Config.StatePath), and a run reads from whichever
// of the two is later. So this caps the first run — pointing the program at an
// organisation with a decade of issues and having it decide to fetch all of
// them is rarely what was meant — and it bounds the cost of a lost cursor or of
// a job whose runs have been failing. Zero means no limit, which is what the
// pass that has to converge on everything wants.
type Since = time.Duration
