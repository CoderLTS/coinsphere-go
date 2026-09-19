// Command coinsphere provides compile-time plugin tooling.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/migration"
	"coinsphere/backend/internal/pluginbuild"
	"coinsphere/backend/internal/pluginlifecycle"
	"coinsphere/backend/internal/pluginregistry"
	"coinsphere/backend/internal/service"
	"coinsphere/backend/plugin/manifest"
	"coinsphere/backend/plugin/official"
	"coinsphere/backend/plugin/sdk"
	"coinsphere/backend/version"
	"gorm.io/gorm"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runContext(ctx, os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "coinsphere: %v\n", err)
		os.Exit(1)
	}
}

func runContext(parent context.Context, args []string, output io.Writer) error {
	if len(args) < 2 {
		return errors.New("usage: coinsphere plugin <...> | workflow <export-legacy|import-v3>")
	}
	if args[0] == "workflow" {
		return transferWorkflow(parent, args[1], args[2:], output)
	}
	if args[0] != "plugin" {
		return errors.New("usage: coinsphere plugin <validate|install|upgrade|uninstall|purge-data>")
	}
	if args[1] == "validate" {
		return validatePlugins(args[2:], output)
	}
	return changePlugin(parent, args[1], args[2:], output)
}

func transferWorkflow(parent context.Context, action string, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("workflow "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "配置文件路径")
	outPath := flags.String("out", "", "导出文件路径")
	inPath := flags.String("in", "", "导入文件路径")
	reportPath := flags.String("report", "", "导入报告路径")
	timeout := flags.Duration("timeout", 10*time.Minute, "操作超时")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *timeout <= 0 {
		return errors.New("timeout must be greater than zero")
	}
	ctx, cancel := context.WithTimeout(parent, *timeout)
	defer cancel()
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	gdb, err := db.Connect(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return fmt.Errorf("get sql database: %w", err)
	}
	defer sqlDB.Close()
	switch action {
	case "export-legacy":
		if strings.TrimSpace(*outPath) == "" {
			return errors.New("workflow export requires --out")
		}
		file, err := os.Create(*outPath)
		if err != nil {
			return fmt.Errorf("create export file: %w", err)
		}
		err = migration.ExportWorkflowTransfer(ctx, sqlDB, file)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return fmt.Errorf("close export file: %w", closeErr)
		}
		_, _ = fmt.Fprintf(output, "workflow definitions exported to %s\n", *outPath)
		return nil
	case "import-v3":
		if strings.TrimSpace(*inPath) == "" || strings.TrimSpace(*reportPath) == "" {
			return errors.New("workflow import requires --in and --report")
		}
		file, err := os.Open(*inPath)
		if err != nil {
			return fmt.Errorf("open import file: %w", err)
		}
		validateGraph, validatorErr := buildWorkflowGraphValidator(ctx, gdb, cfg)
		if validatorErr != nil {
			_ = file.Close()
			return validatorErr
		}
		report, importErr := migration.ImportWorkflowTransfer(ctx, sqlDB, file, migration.TransferOptions{ValidateGraph: validateGraph})
		closeErr := file.Close()
		if closeErr != nil && importErr == nil {
			importErr = fmt.Errorf("close import file: %w", closeErr)
		}
		if err := migration.WriteTransferReport(*reportPath, report); err != nil {
			return err
		}
		if importErr != nil {
			return importErr
		}
		_, _ = fmt.Fprintf(output, "workflow definitions imported; report written to %s\n", *reportPath)
		return nil
	default:
		return fmt.Errorf("unsupported workflow action %q", action)
	}
}

func buildWorkflowGraphValidator(ctx context.Context, gdb *gorm.DB, cfg *config.AppConfig) (func(json.RawMessage) error, error) {
	registry := sdk.NewRegistry()
	app := service.NewApp(gdb, cfg, registry)
	app.Profiles = registry
	host := sdk.Host{
		Stores: sdk.GormPluginStores{Database: gdb}, Network: official.NetworkClientFactory{},
		OutboundProxy: app, Realtime: app, Events: app, MarketData: registry, Execution: registry,
		Strategies: registry, Profiles: registry, AllowedHTTPHosts: cfg.Workflow.HTTPAllowedHosts,
	}
	var enabledIDs []string
	if err := gdb.WithContext(ctx).Table("plugin_installations").Where("source_path = ? AND status = ?", "builtin", "installed").Order("plugin_id").Pluck("plugin_id", &enabledIDs).Error; err != nil {
		return nil, fmt.Errorf("load enabled plugins for workflow import: %w", err)
	}
	enabled := make(map[string]bool, len(enabledIDs))
	for _, id := range enabledIDs {
		enabled[id] = true
	}
	if err := official.RegisterAll(registry, host, enabled); err != nil {
		return nil, fmt.Errorf("register plugins for workflow import: %w", err)
	}
	if err := pluginregistry.RegisterAll(registry, host); err != nil {
		return nil, fmt.Errorf("register plugins for workflow import: %w", err)
	}
	return app.ValidateWorkflowGraph, nil
}

func validatePlugins(paths []string, output io.Writer) error {
	if len(paths) == 0 {
		return errors.New("at least one plugin directory is required")
	}
	plugins, err := manifest.LoadAllWithDependencies(paths, version.Core, version.SDKMajor, version.BuiltinPlugins)
	if err != nil {
		return err
	}
	for _, plugin := range plugins {
		if err := migration.ValidatePluginDirectory(plugin.MigrationsPath); err != nil {
			return fmt.Errorf("plugin %s: %w", plugin.Manifest.ID, err)
		}
		if _, err := migration.PluginSchemaName(plugin.Manifest.ID); err != nil {
			return err
		}
	}
	if _, err := pluginbuild.RenderBackendWithDependencies(plugins, version.BuiltinPlugins); err != nil {
		return err
	}
	if _, err := pluginbuild.RenderFrontendWithDependencies(plugins, version.BuiltinPlugins); err != nil {
		return err
	}
	for _, plugin := range plugins {
		_, _ = fmt.Fprintf(output, "valid plugin %s@%s\n", plugin.Manifest.ID, plugin.Manifest.Version)
	}
	return nil
}

func changePlugin(parent context.Context, action string, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("plugin "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "配置文件路径")
	backendRoot := flags.String("backend-root", ".", "backend 源码目录")
	timeout := flags.Duration("timeout", 10*time.Minute, "操作超时")
	confirmation := flags.String("confirm", "", "purge-data 确认文本")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *timeout <= 0 {
		return errors.New("timeout must be greater than zero")
	}
	positionals := flags.Args()
	if len(positionals) != 1 {
		return fmt.Errorf("plugin %s requires exactly one plugin directory or id", action)
	}
	switch action {
	case "install", "upgrade", "uninstall", "purge-data":
	default:
		return fmt.Errorf("unsupported plugin action %q", action)
	}
	if action != "purge-data" && strings.TrimSpace(*confirmation) != "" {
		return errors.New("confirm is only valid with purge-data")
	}

	ctx, cancel := context.WithTimeout(parent, *timeout)
	defer cancel()
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	gdb, err := db.Connect(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return fmt.Errorf("get sql database: %w", err)
	}
	defer sqlDB.Close()
	coreMigrations, err := migration.New(sqlDB)
	if err != nil {
		return err
	}
	if err := coreMigrations.ValidateCurrent(ctx); err != nil {
		return err
	}
	layout, err := pluginlifecycle.NewLayout(*backendRoot)
	if err != nil {
		return err
	}
	installer := pluginlifecycle.New(pluginlifecycle.Options{
		Layout: layout, DB: sqlDB,
		Rebuild: func(ctx context.Context, layout pluginlifecycle.Layout) error {
			return rebuildApplication(ctx, layout, output)
		},
	})

	target := positionals[0]
	switch action {
	case "install", "upgrade":
		if action == "install" {
			builtin, err := installer.EnableBuiltin(ctx, target)
			if err != nil {
				return err
			}
			if builtin {
				_, _ = fmt.Fprintf(output, "enabled built-in plugin %s; restart the application to load it\n", target)
				return nil
			}
		}
		plugin, err := installer.Install(ctx, target, action == "upgrade")
		if err != nil {
			return err
		}
		pastTense := "installed"
		if action == "upgrade" {
			pastTense = "upgraded"
		}
		_, _ = fmt.Fprintf(output, "%s and rebuilt plugin %s@%s\n", pastTense, plugin.Manifest.ID, plugin.Manifest.Version)
	case "uninstall":
		if err := installer.Uninstall(ctx, target); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(output, "uninstalled plugin %s; data retained\n", target)
	case "purge-data":
		if err := installer.PurgeData(ctx, target, *confirmation); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(output, "purged plugin data %s\n", target)
	}
	return nil
}

func rebuildApplication(ctx context.Context, layout pluginlifecycle.Layout, output io.Writer) error {
	commands := []struct {
		name string
		args []string
		dir  string
	}{
		{name: "go", args: []string{"mod", "tidy"}, dir: layout.BackendRoot},
		{name: "docker", args: []string{"compose", "build", "backend"}, dir: filepath.Dir(layout.BackendRoot)},
	}
	for _, command := range commands {
		cmd := exec.CommandContext(ctx, command.name, command.args...)
		cmd.Dir = command.dir
		cmd.Stdout = output
		cmd.Stderr = output
		if command.name == "docker" && os.Getenv("COINSPHERE_AUTH__SECRET_KEY") == "" {
			cmd.Env = append(os.Environ(), "COINSPHERE_AUTH__SECRET_KEY=plugin-build-only-not-deployed")
		}
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s %s: %w", command.name, strings.Join(command.args, " "), err)
		}
	}
	return nil
}
