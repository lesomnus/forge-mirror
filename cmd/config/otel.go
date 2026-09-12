package config

import (
	"context"

	"github.com/lesomnus/forge-mirror/cmd/version"
	"github.com/lesomnus/mkot"
	"github.com/lesomnus/mkot/mkotx"
	"github.com/lesomnus/mkot/pretty"

	// mkot learns an exporter type from that package's init, so a type the
	// configuration is allowed to name has to be linked in even when no symbol
	// of it is used. Without this, "otlp/gateway" is an unknown type and the
	// program refuses the configuration it was handed.
	_ "github.com/lesomnus/mkot/otlp"
	"github.com/lesomnus/otx"
	"github.com/lesomnus/z"
	"go.opentelemetry.io/otel/attribute"
)

type OtelConfig struct {
	mkot.Config `yaml:",inline"`
}

func (c *OtelConfig) Build(ctx context.Context) (context.Context, *otx.Otx, error) {
	otc := mkot.NewConfig()
	if c != nil {
		otc = &c.Config
	}

	if otc.Processors == nil {
		otc.Processors = map[mkot.Id]mkot.ProcessorConfig{}
	}
	if otc.Exporters == nil {
		otc.Exporters = map[mkot.Id]mkot.ExporterConfig{}
	}
	if otc.Providers == nil {
		otc.Providers = map[mkot.Id]*mkot.ProviderConfig{}
	}

	const ServiceResourceId mkot.Id = "resource/forge-mirror"
	if _, ok := otc.Processors[ServiceResourceId]; !ok {
		otc.Processors[ServiceResourceId] = &mkot.Resource{
			Attributes: []mkot.Attr{
				{Key: "service.name", Value: attribute.StringValue("forge-mirror")},
				{Key: "service.version", Value: attribute.StringValue(version.Get().Version)},
			},
		}
	}
	if _, ok := otc.Exporters["pretty"]; !ok {
		otc.Exporters["pretty"] = pretty.ExporterConfig{}
	}
	if _, ok := otc.Providers["tracer"]; !ok {
		otc.Providers["tracer"] = &mkot.ProviderConfig{
			Processors: []mkot.Id{ServiceResourceId},
		}
	}
	if _, ok := otc.Providers["meter"]; !ok {
		otc.Providers["meter"] = &mkot.ProviderConfig{
			Processors: []mkot.Id{ServiceResourceId},
		}
	}
	if _, ok := otc.Providers["logger"]; !ok {
		otc.Providers["logger"] = &mkot.ProviderConfig{
			Exporters: []mkot.Id{"pretty"},
		}
	}

	// Every signal the configuration describes, in one value. A signal it says
	// nothing about is off rather than an error.
	o, err := mkotx.FromConfig(ctx, otc, "",
		// Instruments are attributed to this app rather than to the library
		// that happens to create them.
		otx.WithScopeName("github.com/lesomnus/forge-mirror"),
	)
	if err != nil {
		return nil, nil, z.Err(err, "build telemetry")
	}

	return otx.Into(ctx, o), o, nil
}
