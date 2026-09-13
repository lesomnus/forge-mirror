package github

import "github.com/lesomnus/forge-mirror/forge"

// Both ports, asserted where it costs nothing: a method that drifts out of
// shape should fail here rather than at the call site.
var (
	_ forge.Source     = (*Source)(nil)
	_ forge.Discoverer = (*Source)(nil)
	_ forge.Target     = (*Target)(nil)
)
