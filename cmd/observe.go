package cmd

import (
	"context"
	"errors"
	"time"

	"github.com/lesomnus/forge-mirror/mirror"
	"github.com/lesomnus/otx"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// observe records what a run did.
//
// Counters rather than observable gauges: this is a batch job that exits, so
// there is nobody to observe when a scrape arrives. Where the numbers go is the
// operator's business — declare an exporter in the configuration and they go
// there; declare none and nothing is emitted, which is right on a workstation.
//
// Reported at the end rather than as the run goes, because a counter added to
// mid-run and then interrupted tells a story that never finished, and this
// process is interrupted routinely.
func observe(ctx context.Context, r mirror.Result, took time.Duration) error {
	m := otx.Meter(ctx)

	var errs []error
	ctr := func(name, desc, unit string) metric.Int64Counter {
		c, err := m.Int64Counter(name, metric.WithDescription(desc), metric.WithUnit(unit))
		if err != nil {
			errs = append(errs, err)
		}
		return c
	}

	repos := ctr("forge_mirror.repos", "Repositories considered, by outcome.", "{repository}")
	issues := ctr("forge_mirror.issues", "Issues and pull requests written, by what was done to them.", "{issue}")
	notes := ctr("forge_mirror.notes", "Comments written.", "{comment}")

	duration, err := m.Float64Histogram("forge_mirror.duration",
		metric.WithDescription("Duration of a run."), metric.WithUnit("s"))
	if err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	// The count that matters for an alert is failed: a run that mirrors nothing
	// because nothing changed is the normal one and must not look like trouble.
	repos.Add(ctx, int64(r.Repos-r.Failed), metric.WithAttributes(attribute.String("outcome", "ok")))
	repos.Add(ctx, int64(r.Failed), metric.WithAttributes(attribute.String("outcome", "failed")))

	issues.Add(ctx, int64(r.Created), metric.WithAttributes(attribute.String("action", "created")))
	issues.Add(ctx, int64(r.Updated), metric.WithAttributes(attribute.String("action", "updated")))

	notes.Add(ctx, int64(r.Notes))
	duration.Record(ctx, took.Seconds())

	return nil
}
