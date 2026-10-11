package main

import (
	"context"
	"flag"
	"fmt"
	modelaccess "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/modelaccess"
	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/entrypoints/assembly"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

func run() error {
	action := flag.String("action", "", "init-storage, register-provider-budget, publish-projection, prepare-evidence, extract-candidates, compile-evidence, prepare-review, render-review, compile-calendar or export-evidence (no trades)")
	source := flag.String("config", "config/config.toml", "Canonical private config for this operator command only")
	version := flag.Int64("expected-version", -1, "Existing read version; -1 only for initial publication")
	proposalInput := flag.String("proposal-input", "", "Private closed JSON evidence and catalog request")
	proposalOutput := flag.String("proposal-output", "", "New private JSON file under this repository runtime")
	proposalBytes := flag.Int("max-proposal-bytes", 0, "Explicit input and output file byte budget")
	semanticInput := flag.String("semantic-input", "", "Optional private semantic artifact for render-review only")
	flag.Parse()
	semanticProvided := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "semantic-input" {
			semanticProvided = true
		}
	})
	if semanticProvided && (*action != "render-review" || *semanticInput == "") {
		return d.Fail("SEMANTIC_REVIEW_INVALID", 422)
	}
	if *action == "extract-candidates" {
		root, err := os.Getwd()
		if err != nil {
			return d.Fail("SEMANTIC_CONFIG_INVALID", 503)
		}
		path, err := filepath.Abs(*source)
		if err != nil {
			return d.Fail("SEMANTIC_CONFIG_INVALID", 503)
		}
		settings, err := config.LoadSemantic(root, path)
		if err != nil {
			return err
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		client := modelaccess.Client{Root: root, Config: path, Settings: settings}
		return operations.ExtractSemanticFile(ctx, client, root, *proposalInput, *proposalOutput, *proposalBytes, evidence.SemanticLimits{MaxEvents: settings.MaxEvents, MaxQuoteBytes: settings.MaxQuoteBytes})
	}
	if d.Has([]string{"prepare-evidence", "compile-evidence", "prepare-review", "render-review", "compile-calendar"}, *action) {
		root, err := os.Getwd()
		if err != nil {
			return d.Fail("PROPOSAL_FILE_INVALID", 422)
		}
		if *action == "compile-evidence" {
			return operations.CompileEvidenceFile(root, *proposalInput, *proposalOutput, *proposalBytes, time.Now().UTC())
		}
		if *action == "compile-calendar" {
			return operations.CompileVenueCalendarFile(root, *proposalInput, *proposalOutput, *proposalBytes, time.Now().UTC())
		}
		if *action == "prepare-review" {
			return operations.PrepareReviewBundleFile(root, *proposalInput, *proposalOutput, *proposalBytes)
		}
		if *action == "render-review" {
			if semanticProvided {
				return operations.RenderReviewBundleWithSemanticFile(root, *proposalInput, *semanticInput, *proposalOutput, *proposalBytes)
			}
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
	if *action != "init-storage" && *action != "publish-projection" && *action != "register-provider-budget" {
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
	if *action == "register-provider-budget" {
		root, err := os.Getwd()
		if err != nil {
			return d.Fail("PROVIDER_REGISTRATION_INVALID", 422)
		}
		registration, err := config.LoadProviderBudgetRegistration(root, *proposalInput, *proposalBytes)
		if err != nil {
			return err
		}
		if err = pg.ConfigureProviderPool(ctx, publication.DatabaseURL, registration.Policy); err != nil {
			return err
		}
		for _, grant := range registration.Grants {
			if err = pg.GrantProviderAccess(ctx, publication.DatabaseURL, grant); err != nil {
				return err
			}
		}
		return nil
	}
	if *action == "init-storage" {
		if err = pg.InitializeProviderControl(ctx, publication.DatabaseURL); err != nil {
			return err
		}
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
