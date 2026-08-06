// Command admin 提供一次性的管理员凭据恢复操作。
package main

import (
	"bufio"
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
	"coinsphere/backend/internal/security"
	"golang.org/x/term"
	"gorm.io/gorm"
)

const (
	adminCommandTimeout   = 30 * time.Second
	minimumPasswordLength = 12
)

type options struct {
	configPath string
	username   string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "admin command failed: %v\n", err)
		os.Exit(1)
	}
}

func run(parent context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	opts, err := parseOptions(args, stderr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, adminCommandTimeout)
	defer cancel()

	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	database, err := db.Connect(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		return fmt.Errorf("get sql database: %w", err)
	}
	defer sqlDB.Close()
	runner, err := migration.New(sqlDB)
	if err != nil {
		return err
	}
	if err := runner.ValidateCurrent(ctx); err != nil {
		return fmt.Errorf("validate database schema: %w", err)
	}

	password, err := readConfirmedPassword(stdin, stderr)
	if err != nil {
		return err
	}
	if err := resetPassword(ctx, database, security.NewPasswordHasher(cfg.Auth.PasswordIterations), opts.username, password); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "password reset")
	return nil
}

func parseOptions(args []string, output io.Writer) (options, error) {
	var opts options
	flags := flag.NewFlagSet("admin", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&opts.configPath, "config", "", "配置文件路径")
	flags.StringVar(&opts.username, "username", "", "需要重置密码的用户名")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, errors.New("unexpected positional arguments")
	}
	opts.username = strings.TrimSpace(opts.username)
	if opts.username == "" {
		return options{}, errors.New("username is required")
	}
	return opts, nil
}

// readConfirmedPassword 只从标准输入读取密码，避免命令行参数和进程列表泄露凭据。
func readConfirmedPassword(input io.Reader, output io.Writer) (string, error) {
	var read func() (string, error)
	if file, ok := input.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		read = func() (string, error) {
			raw, err := term.ReadPassword(int(file.Fd()))
			_, _ = fmt.Fprintln(output)
			if err != nil {
				return "", errors.New("read password from terminal")
			}
			return string(raw), nil
		}
	} else {
		reader := bufio.NewReader(input)
		read = func() (string, error) { return readPasswordLine(reader) }
	}
	_, _ = fmt.Fprint(output, "new password: ")
	first, err := read()
	if err != nil {
		return "", err
	}
	_, _ = fmt.Fprint(output, "confirm password: ")
	second, err := read()
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("password confirmation does not match")
	}
	if len([]rune(first)) < minimumPasswordLength {
		return "", fmt.Errorf("password must contain at least %d characters", minimumPasswordLength)
	}
	return first, nil
}

func readPasswordLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", errors.New("read password from standard input")
	}
	if errors.Is(err, io.EOF) && line == "" {
		return "", errors.New("password input ended unexpectedly")
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

func resetPassword(
	ctx context.Context,
	database *gorm.DB,
	hasher *security.PasswordHasher,
	username string,
	password string,
) error {
	return database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user db.SystemUser
		if err := tx.Where("username = ?", username).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("user not found")
			}
			return err
		}
		now := time.Now()
		if err := tx.Model(&db.SystemUser{}).Where("id = ?", user.ID).Updates(map[string]any{
			"password_hash": hasher.HashPassword(password),
			"updated_at":    now, "updated_by": "admin-cli",
		}).Error; err != nil {
			return err
		}
		return nil
	})
}
