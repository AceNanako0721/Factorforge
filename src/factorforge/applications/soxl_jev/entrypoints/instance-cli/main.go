package main

import (
	"context"
	"flag"
	"fmt"
	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/entrypoints/assembly"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func run() error {
	action := flag.String("action", "", "init-storage or publish-projection (no transactions)")
	source := flag.String("config", "config/config.toml", "Canonical private config for this operator command only")
	version := flag.Int64("expected-version", -1, "Existing read version; -1 only for initial publication")
	flag.Parse()
	if *action != "init-storage" && *action != "publish-projection" {
		return d.Fail("INSTANCE_CLI_ACTION_REQUIRED", 422)
	}
	path, err := filepath.Abs(*source)
	if err != nil {
		return d.Fail("INSTANCE_CONFIG_PATH_INVALID", 422)
	}
	profile, publication, err := config.LoadPublication(path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(profile.Settings.TimeoutSeconds)*time.Second)
	defer cancel()
	if *action == "init-storage" {
		if err = pg.InitializePipeline(ctx, publication.DatabaseURL, profile.Binding()); err != nil {
			return err
		}
		return pg.Initialize(ctx, publication.DatabaseURL, profile.Binding())
	}
	assets, err := config.LoadPipelineAssets(profile)
	if err != nil {
		return err
	}
	store, err := pg.OpenPipeline(ctx, profile.Access.DatabaseURL, profile.Binding(), "INGEST", profile.Settings.MaxInputBytes)
	if err != nil {
		return err
	}
	defer store.Close()
	ids := []string{}
	for id := range assets.Sources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	sources := []d.SourceRegistration{}
	for _, id := range ids {
		sources = append(sources, assets.Sources[id])
	}
	next := int64(0)
	var expected *int64
	if *version >= 0 {
		next = *version + 1
		expected = version
	} else if *version != -1 {
		return d.Fail("INSTANCE_VERSION_REQUIRED", 422)
	}
	snapshot, originals, err := store.Projection(ctx, pg.ProjectionOptions{Version: next, SourceVersion: publication.SourceVersion, Stage: profile.Settings.Stage, Sources: sources, FixtureOnly: profile.Mode == "mock", Now: time.Now().UTC(), MaxRecords: publication.MaxRecords, MaxBytes: publication.MaxBytes})
	if err != nil {
		return err
	}
	return pg.PublishPipeline(ctx, publication.DatabaseURL, snapshot, originals, expected)
}
func main() {
	if err := run(); err != nil {
		assembly.PrintError(err)
		os.Exit(1)
	}
	fmt.Println("INSTANCE_OPERATOR_OPERATION_RECORDED")
}
