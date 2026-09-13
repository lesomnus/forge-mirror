package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
	"github.com/lesomnus/mkot"
	"github.com/lesomnus/z"
)

var DefaultConfigPaths = []string{
	"forge-mirror.yaml",
	"forge-mirror.yml",
}

type Config struct {
	path string

	Source SourceConfig `yaml:"source"`
	Target TargetConfig `yaml:"target"`

	// Direction is which way this run copies. Empty is the mirror.
	Direction Direction `yaml:"direction"`

	Since  Since `yaml:"since"`
	DryRun bool  `yaml:"dryRun"`

	// StatePath is the file the cursor is kept in between runs. Empty disables
	// it, and then every run looks back as far as Since allows.
	//
	// What is on it is an optimisation, never the record of what was mirrored:
	// that is read back out of the target. So this may live on a disk that can
	// be wiped — losing it costs one expensive run.
	StatePath string `yaml:"statePath"`

	Otel OtelConfig
}

func ReadFromFile(p string) (*Config, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, z.Err(err, "open")
	}

	// "${env:NAME}" and "${env:NAME:-default}" are resolved before the file is
	// read, the way the OpenTelemetry Collector resolves them, so a secret can
	// be named in the file without being written in it. A name that is neither
	// set nor given a default is an error rather than an empty string. Write
	// "$$" for a literal dollar sign.
	b, err = mkot.ExpandEnv(b)
	if err != nil {
		return nil, z.Err(err, "expand")
	}

	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, z.Err(err, "decode")
	}

	c.path = p
	return &c, nil
}

func (c *Config) Path() string {
	return c.path
}

func (c *Config) Evaluate() error {
	if c.Direction == "" {
		c.Direction = GitHubToGitLab
	}
	switch c.Direction {
	case GitHubToGitLab, GitLabToGitHub:
	default:
		return fmt.Errorf("direction must be %q or %q, not %q",
			GitHubToGitLab, GitLabToGitHub, c.Direction)
	}

	// The target group defaults to the source owner. Mirroring acme/* into a
	// group named something else is possible and occasionally wanted, but the
	// same name is what anyone would assume from looking at either side.
	z.FallbackP(&c.Target.Group, c.Source.Owner)

	if c.Source.Owner == "" {
		return errors.New("source.owner is required")
	}
	if c.Source.Token == "" {
		return errors.New("source.token is required")
	}
	if c.Target.Token == "" {
		return errors.New("target.token is required")
	}

	return nil
}
