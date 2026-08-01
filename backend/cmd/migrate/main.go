// Command migrate applies the backend's embedded versioned SQL migrations.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/migration"
)

type options struct {
	configPath string
	direction  string
	steps      int
	target     int64
	timeout    time.Duration
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "migration failed: %v\n", err)
		os.Exit(1)
	}
}

func run(parent context.Context, args []string, stdout, stderr io.Writer) error {
	opts, err := parseOptions(args, stderr)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(parent, opts.timeout)
	defer cancel()

	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	gdb, err := db.Connect(cfg.Database)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return fmt.Errorf("get sql database: %w", err)
	}
	defer sqlDB.Close()

	runner, err := migration.New(sqlDB, cfg.Database.Driver)
	if err != nil {
		return err
	}
	return execute(ctx, runner, opts, stdout)
}

func parseOptions(args []string, output io.Writer) (options, error) {
	var opts options
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&opts.configPath, "config", "", "配置文件路径")
	flags.StringVar(&opts.direction, "direction", "up", "操作: up、down、version 或 status")
	flags.IntVar(&opts.steps, "steps", 1, "down 回滚步数")
	flags.Int64Var(&opts.target, "target", 0, "up 的目标版本，0 表示最新")
	flags.DurationVar(&opts.timeout, "timeout", 5*time.Minute, "迁移超时")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}

	opts.direction = strings.ToLower(strings.TrimSpace(opts.direction))
	switch opts.direction {
	case "up":
		if opts.target < 0 {
			return options{}, errors.New("target must be zero or greater")
		}
	case "down":
		if opts.steps < 1 {
			return options{}, errors.New("steps must be at least one")
		}
		if opts.target != 0 {
			return options{}, errors.New("target is only valid with direction=up")
		}
	case "version", "status":
		if opts.target != 0 {
			return options{}, errors.New("target is only valid with direction=up")
		}
	default:
		return options{}, fmt.Errorf("unsupported direction %q", opts.direction)
	}
	if opts.timeout <= 0 {
		return options{}, errors.New("timeout must be greater than zero")
	}
	return opts, nil
}

func execute(ctx context.Context, runner *migration.Runner, opts options, output io.Writer) error {
	switch opts.direction {
	case "up":
		results, err := runner.Up(ctx, opts.target)
		for _, result := range results {
			_, _ = fmt.Fprintf(output, "applied %s migration %d (%s)\n", result.Direction, result.Version, result.Duration)
		}
		if err != nil {
			return err
		}
		current, latest, err := runner.Versions(ctx)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(output, "migration version: current=%d latest=%d applied=%d\n", current, latest, len(results))
		return nil
	case "down":
		results, err := runner.Down(ctx, opts.steps)
		for _, result := range results {
			_, _ = fmt.Fprintf(output, "applied %s migration %d (%s)\n", result.Direction, result.Version, result.Duration)
		}
		if err != nil {
			return err
		}
		current, latest, err := runner.Versions(ctx)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(output, "migration version: current=%d latest=%d rolled_back=%d\n", current, latest, len(results))
		return nil
	case "version":
		current, latest, err := runner.Versions(ctx)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(output, "migration version: current=%d latest=%d\n", current, latest)
		return nil
	case "status":
		statuses, err := runner.Status(ctx)
		if err != nil {
			return err
		}
		for _, status := range statuses {
			appliedAt := "-"
			if !status.AppliedAt.IsZero() {
				appliedAt = status.AppliedAt.UTC().Format(time.RFC3339Nano)
			}
			_, _ = fmt.Fprintf(output, "%05d\t%s\t%s\t%s\n", status.Version, status.State, appliedAt, status.Name)
		}
		return nil
	default:
		return fmt.Errorf("unsupported direction %q", opts.direction)
	}
}
