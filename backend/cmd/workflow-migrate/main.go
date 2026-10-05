// workflow-migrate imports definitions into an isolated generation 4 database.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/migration"
	"coinsphere/backend/internal/pluginregistry"
	"coinsphere/backend/internal/security"
	"coinsphere/backend/internal/service"
	"coinsphere/backend/internal/workflowmigration"
	"coinsphere/backend/plugin/official"
	"coinsphere/backend/plugin/sdk"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "workflow migration:", err)
		os.Exit(1)
	}
}
func run(parent context.Context) error {
	command := flag.String("command", "inspect", "inspect, plan, apply, verify")
	targetConfig := flag.String("target-config", "", "explicit generation 4 target configuration")
	sourceEnv := flag.String("source-dsn-env", "COINSPHERE_MIGRATION_SOURCE_DSN", "environment variable containing the read-only source DSN")
	sourceKeyEnv := flag.String("source-key-env", "COINSPHERE_MIGRATION_SOURCE_KEY", "environment variable containing the old non-trading cipher key")
	sourceID := flag.String("source-id", "", "explicit source instance identity label")
	mappingFile := flag.String("mappings", "", "reviewed JSON mappings; no credential values")
	planFile := flag.String("plan", "", "reviewed plan file")
	reportFile := flag.String("output", "", "report file; defaults to standard output")
	stopped := flag.Bool("source-stopped", false, "confirm the source application, triggers, and writers have stopped for apply")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *command != "inspect" && *command != "plan" && *command != "apply" && *command != "verify" {
		return errors.New("unsupported migration command")
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	var plan workflowmigration.Plan
	if *command == "apply" || *command == "verify" {
		if *planFile == "" || readJSON(*planFile, &plan) != nil {
			return errors.New("an intact reviewed plan file is required")
		}
		if err := plan.Validate(); err != nil {
			return err
		}
	}
	if *command == "apply" && !*stopped {
		return errors.New("apply requires explicit source-stopped acknowledgement after the maintenance-window shutdown")
	}
	var source workflowmigration.Snapshot
	if *command != "verify" {
		if os.Getenv(*sourceEnv) == "" {
			return errors.New("source DSN environment variable is missing")
		}
		sourceDB, err := sql.Open("pgx", os.Getenv(*sourceEnv))
		if err != nil {
			return errors.New("invalid source DSN")
		}
		defer sourceDB.Close()
		source, err = workflowmigration.ReadSource(ctx, sourceDB, *sourceID)
		if err != nil {
			return err
		}
	}
	if *command == "inspect" {
		return writeReport(*reportFile, source.Inspect())
	}
	if *targetConfig == "" {
		return errors.New("an explicit target configuration file is required")
	}
	cfg, err := config.Load(*targetConfig)
	if err != nil {
		return errors.New("cannot read target configuration")
	}
	gdb, err := db.Connect(ctx, cfg.Database)
	if err != nil {
		return errors.New("cannot connect to target database")
	}
	target, err := gdb.DB()
	if err != nil {
		return errors.New("cannot open target connection")
	}
	defer target.Close()
	validator, err := migration.New(target)
	if err != nil {
		return err
	}
	if err := validator.ValidateCurrent(ctx); err != nil {
		return err
	}
	identity, err := workflowmigration.TargetIdentity(ctx, target)
	if err != nil {
		return err
	}
	if *command == "verify" {
		result, err := workflowmigration.Verify(ctx, target, plan)
		if err != nil {
			return err
		}
		return writeReport(*reportFile, result)
	}
	registry := sdk.NewRegistry()
	app := service.NewApp(gdb, cfg, registry)
	enabled := map[string]bool{}
	var installations []struct{ PluginID, Status string }
	if err := gdb.Table("plugin_installations").Find(&installations).Error; err != nil {
		return errors.New("cannot read target installations")
	}
	for _, installation := range installations {
		enabled[installation.PluginID] = installation.Status == "installed"
	}
	host := sdk.Host{Inbox: app, Stores: sdk.GormPluginStores{Database: gdb}, Network: official.NetworkClientFactory{}, OutboundProxy: app, Realtime: app, Events: app, AllowedHTTPHosts: cfg.Workflow.HTTPAllowedHosts}
	if err := official.RegisterAll(registry, host, enabled); err != nil {
		return errors.New("target official plugin registration failed")
	}
	if err := pluginregistry.RegisterAll(registry, host, enabled); err != nil {
		return errors.New("target compiled plugin registration failed")
	}
	catalog := workflowmigration.Catalog{Converter: workflowmigration.Converter{Catalog: app.WorkflowNodeCatalog(), NodePlugins: map[string]string{}, Validate: app.ValidateWorkflowGraph}, Plugins: registry.Plugins(), Pages: map[string]sdk.ResultPageDescriptor{}, Permissions: map[string]sdk.PermissionDescriptor{}}
	for node := range catalog.Converter.Catalog {
		catalog.Converter.NodePlugins[node] = registry.NodePlugin(node)
	}
	for _, plugin := range registry.Plugins() {
		for _, page := range registry.PluginResultPages(plugin.ID) {
			catalog.Pages[plugin.ID+"/"+page.PageKey] = page
		}
	}
	for code, p := range app.CapabilityCatalog() {
		catalog.Permissions[code] = sdk.PermissionDescriptor{Code: code, Title: p.Title, Protected: p.Protected}
	}
	if *command == "plan" {
		var mappings workflowmigration.Mappings
		if *mappingFile != "" && readJSON(*mappingFile, &mappings) != nil {
			return errors.New("invalid explicit mappings file")
		}
		plan = workflowmigration.BuildPlan(source, identity, catalog, mappings)
		if *planFile == "" {
			return errors.New("plan requires a destination plan file")
		}
		if err := writeReport(*planFile, plan); err != nil {
			return err
		}
		if err := writeReport(*reportFile, map[string]any{"planHash": plan.Hash, "workflows": len(plan.Workflows), "issues": plan.Issues, "dependencies": plan.Dependencies}); err != nil {
			return err
		}
		if len(plan.Issues) > 0 {
			return errors.New("plan saved with unresolved conversion items; apply is blocked")
		}
		return nil
	}
	oldCipher, err := security.NewSecretCipher(os.Getenv(*sourceKeyEnv))
	if err != nil || os.Getenv(*sourceKeyEnv) == "" || app.Cipher == nil {
		return errors.New("source and target server-side non-trading cipher keys are required")
	}
	result, err := workflowmigration.Apply(ctx, target, source, plan, catalog, func(ciphertext string) (string, error) {
		plain, err := oldCipher.Decrypt(ciphertext)
		if err != nil {
			return "", errors.New("opaque secret cannot be rebound")
		}
		encrypted := app.Cipher.Encrypt(plain)
		if encrypted == "" {
			return "", errors.New("opaque secret cannot be encrypted")
		}
		return encrypted, nil
	})
	if err != nil {
		return err
	}
	return writeReport(*reportFile, result)
}
func readJSON(path string, value any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if errors.Is(decoder.Decode(&trailing), io.EOF) {
		return nil
	}
	return errors.New("trailing data")
}
func writeReport(path string, value any) error {
	if path == "" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.New("cannot encode sanitized migration report")
	}
	// Atomic replacement prevents a partially written plan from being reviewed.
	file, err := os.CreateTemp(filepath.Dir(path), ".workflow-plan-*")
	if err != nil {
		return errors.New("cannot create migration report")
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return errors.New("cannot protect migration report")
	}
	if _, err := file.Write(append(raw, '\n')); err != nil {
		file.Close()
		return errors.New("cannot write migration report")
	}
	if err := file.Close(); err != nil {
		return errors.New("cannot close migration report")
	}
	if err := os.Rename(name, path); err != nil {
		return errors.New("cannot replace migration report")
	}
	return nil
}
