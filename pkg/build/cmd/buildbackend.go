package main

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v2"

	"github.com/grafana/grafana/pkg/build/compilers"
	"github.com/grafana/grafana/pkg/build/config"
	"github.com/grafana/grafana/pkg/build/errutil"
	"github.com/grafana/grafana/pkg/build/grafana"
	"github.com/grafana/grafana/pkg/build/syncutil"
)

func BuildBackend(ctx *cli.Context) error {
	metadata, err := config.GetMetadata(filepath.Join("dist", "version.json"))
	if err != nil {
		return err
	}

	var version string

	// ./ci build-backend v1.0.0
	if ctx.NArg() == 1 {
		version = strings.TrimPrefix(ctx.Args().Get(0), "v")
	} else {
		version = metadata.GrafanaVersion
	}

	var (
		edition = config.Edition(ctx.String("edition"))
		cfg     = config.Config{
			NumWorkers: ctx.Int("jobs"),
		}
	)

	mode, err := config.GetVersion(metadata.ReleaseMode)
	if err != nil {
		return fmt.Errorf("could not get version / package info for mode '%s': %w", metadata.ReleaseMode, err)
	}

	const grafanaDir = "."

	log.Printf("Building Grafana back-end, version %q, %s edition, variants [%v]",
		version, edition, mode.Variants)

	p := syncutil.NewWorkerPool(cfg.NumWorkers)
	defer p.Close()

	if err := compilers.Install(); err != nil {
		return cli.Exit(err.Error(), 1)
	}

	g, _ := errutil.GroupWithContext(ctx.Context)
	for _, variant := range mode.Variants {
		variant := variant

		opts := grafana.BuildVariantOpts{
			Variant:    variant,
			Edition:    edition,
			Version:    version,
			GrafanaDir: grafanaDir,
		}

		p.Schedule(g.Wrap(func() error {
			return grafana.BuildVariant(ctx.Context, opts)
		}))
	}
	if err := g.Wait(); err != nil {
		return cli.Exit(err.Error(), 1)
	}

	log.Println("Successfully built back-end binaries!")
	return nil
}
