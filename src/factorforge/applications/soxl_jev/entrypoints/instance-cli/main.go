package main

import (
	"context"
	"flag"
	"fmt"
	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/entrypoints/assembly"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func run() error {
	action := flag.String("action", "", "init-storage, publish-projection, prepare-evidence, compile-evidence, prepare-review, render-review or export-evidence (no trades)")
	source := flag.String("config", "config/config.toml", "Canonical private config for this operator command only")
	version := flag.Int64("expected-version", -1, "Existing read version; -1 only for initial publication")
	proposalInput := flag.String("proposal-input", "", "Private closed JSON evidence and catalog request")
	proposalOutput := flag.String("proposal-output", "", "New private JSON file under this repository runtime")
	proposalBytes := flag.Int("max-proposal-bytes", 0, "Explicit input and output file byte budget")
	flag.Parse()
	if d.Has([]string{"prepare-evidence", "compile-evidence", "prepare-review", "render-review"}, *action) {
		root, err := os.Getwd()
		if err != nil {
			return d.Fail("PROPOSAL_FILE_INVALID", 422)
		}
		if *action == "compile-evidence" {
			return operations.CompileEvidenceFile(root, *proposalInput, *proposalOutput, *proposalBytes, time.Now().UTC())
		}
		if *action == "prepare-review" {
			return operations.PrepareReviewBundleFile(root, *proposalInput, *proposalOutput, *proposalBytes)
		}
		if *action == "render-review" {
			return operations.RenderReviewBundleFile(root, *proposalInput, *proposalOutput, *proposalBytes)
		}
		return operations.PrepareEvidenceFile(root, *proposalInput, *proposalOutput, *proposalBytes)
	}
	if *action == "export-evidence" {
		root, err := os.Getwd()
		if err != nil {
			return d.Fail("PROPOSAL_FILE_INVALID", 422)
		}
		path, err := filepath.Abs(*source)
		if err != nil {
			return d.Fail("INSTANCE_CONFIG_PATH_INVALID", 422)
		}
		profile, err := config.LoadWorker(path, "INGEST")
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(profile.Settings.TimeoutSeconds)*time.Second)
		defer cancel()
		store, err := pg.OpenPipeline(ctx, profile.Access.DatabaseURL, profile.Binding(), "INGEST", profile.Settings.MaxInputBytes)
		if err != nil {
			return err
		}
		defer store.Close()
		return operations.ExportReviewBundleFile(ctx, store, profile.Binding(), root, *proposalInput, *proposalOutput, *proposalBytes)
	}
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
