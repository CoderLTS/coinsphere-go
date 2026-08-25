// Command coinsphere provides compile-time plugin tooling.
package main

import (
	"context"
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
	"coinsphere/backend/plugin/manifest"
	"coinsphere/backend/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runContext(ctx, os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "coinsphere: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	return runContext(context.Background(), args, output)
}

func runContext(parent context.Context, args []string, output io.Writer) error {
	if len(args) < 2 || args[0] != "plugin" {
		return errors.New("usage: coinsphere plugin <validate|install|upgrade|uninstall|purge-data>")
	}
	if args[1] == "validate" {
		return validatePlugins(args[2:], output)
	}
	return changePlugin(parent, args[1], args[2:], output)
}

func validatePlugins(paths []string, output io.Writer) error {
	if len(paths) == 0 {
		return errors.New("at least one plugin directory is required")
	}
	plugins, err := manifest.LoadAll(paths, version.Core, version.SDKMajor)
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
	if _, err := pluginbuild.RenderBackend(plugins); err != nil {
		return err
	}
	if _, err := pluginbuild.RenderFrontend(plugins); err != nil {
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
		_, _ = fmt.Fprintf(output, "uninstalled and rebuilt plugin %s; data retained\n", target)
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
		{name: "docker", args: []string{"compose", "build", "backend", "web"}, dir: filepath.Dir(layout.BackendRoot)},
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
