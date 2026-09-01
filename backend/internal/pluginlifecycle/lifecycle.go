// Package pluginlifecycle 管理会修改源码输入、但不执行运行时加载的本地插件生命周期。
package pluginlifecycle

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	semver "github.com/Masterminds/semver/v3"
	"github.com/pressly/goose/v3"
	"golang.org/x/mod/modfile"

	"coinsphere/backend/internal/migration"
	"coinsphere/backend/internal/pluginbuild"
	"coinsphere/backend/plugin/manifest"
	"coinsphere/backend/version"
)

type Layout struct {
	BackendRoot  string
	FrontendRoot string
}

func NewLayout(backendRoot string) (Layout, error) {
	root, err := filepath.Abs(backendRoot)
	if err != nil {
		return Layout{}, fmt.Errorf("resolve backend root: %w", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return Layout{}, fmt.Errorf("read backend go.mod: %w", err)
	}
	module, err := modfile.Parse("go.mod", raw, nil)
	if err != nil || module.Module == nil || module.Module.Mod.Path != "coinsphere/backend" {
		return Layout{}, errors.New("backend root must contain the coinsphere/backend module")
	}
	frontend := filepath.Join(filepath.Dir(root), "frontend")
	if _, err := os.Stat(filepath.Join(frontend, "package.json")); err != nil {
		return Layout{}, fmt.Errorf("find sibling frontend: %w", err)
	}
	return Layout{BackendRoot: root, FrontendRoot: frontend}, nil
}

func (l Layout) backendInstalledRoot() string {
	return filepath.Join(l.BackendRoot, "plugin", "installed")
}

func (l Layout) frontendInstalledRoot() string {
	return filepath.Join(l.FrontendRoot, "src", "plugins", "installed")
}

func (l Layout) registryBackendPath() string {
	return filepath.Join(l.BackendRoot, "internal", "pluginregistry", "registry.generated.go")
}

func (l Layout) registryFrontendPath() string {
	return filepath.Join(l.FrontendRoot, "src", "plugins", "registry.generated.ts")
}

func (l Layout) goModPath() string { return filepath.Join(l.BackendRoot, "go.mod") }

func (l Layout) goSumPath() string { return filepath.Join(l.BackendRoot, "go.sum") }

type Options struct {
	Layout      Layout
	DB          *sql.DB
	CoreVersion string
	SDKMajor    int
	Rebuild     func(context.Context, Layout) error
}

func (o Options) normalized() Options {
	if o.CoreVersion == "" {
		o.CoreVersion = version.Core
	}
	if o.SDKMajor == 0 {
		o.SDKMajor = version.SDKMajor
	}
	return o
}

// ponytail: 同一 checkout 每次只运行一个维护命令；出现并发运维需求时再加跨进程锁。
type Installer struct{ options Options }

func New(options Options) Installer { return Installer{options: options.normalized()} }

func (i Installer) Validate(root string) (manifest.Package, error) {
	pkg, err := manifest.Load(root, i.options.CoreVersion, i.options.SDKMajor)
	if err != nil {
		return manifest.Package{}, err
	}
	if err := migration.ValidatePluginDirectory(pkg.MigrationsPath); err != nil {
		return manifest.Package{}, err
	}
	if _, err := migration.PluginSchemaName(pkg.Manifest.ID); err != nil {
		return manifest.Package{}, err
	}
	return pkg, nil
}

func (i Installer) Installed() ([]manifest.Package, error) {
	root := i.options.Layout.backendInstalledRoot()
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list installed plugins: %w", err)
	}
	roots := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || strings.HasSuffix(entry.Name(), ".old") || strings.HasSuffix(entry.Name(), ".uninstall-backup") {
			continue
		}
		roots = append(roots, filepath.Join(root, entry.Name()))
	}
	packages, err := manifest.LoadAllWithDependencies(roots, i.options.CoreVersion, i.options.SDKMajor, version.BuiltinPlugins)
	if err != nil {
		return nil, err
	}
	for _, pkg := range packages {
		if err := migration.ValidatePluginDirectory(pkg.MigrationsPath); err != nil {
			return nil, fmt.Errorf("plugin %s: %w", pkg.Manifest.ID, err)
		}
	}
	return packages, nil
}

func (i Installer) Install(ctx context.Context, source string, upgrade bool) (result manifest.Package, returnErr error) {
	candidate, err := i.Validate(source)
	if err != nil {
		return manifest.Package{}, err
	}
	installed, err := i.Installed()
	if err != nil {
		return manifest.Package{}, err
	}
	var previous *manifest.Package
	for index := range installed {
		if installed[index].Manifest.ID == candidate.Manifest.ID {
			previous = &installed[index]
			break
		}
	}
	if previous != nil && !upgrade {
		return manifest.Package{}, fmt.Errorf("plugin %q is already installed; use upgrade", candidate.Manifest.ID)
	}
	if previous != nil {
		oldVersion, _ := semver.NewVersion(previous.Manifest.Version)
		newVersion, _ := semver.NewVersion(candidate.Manifest.Version)
		if !newVersion.GreaterThan(oldVersion) {
			return manifest.Package{}, fmt.Errorf("upgrade %q must increase version from %s", candidate.Manifest.ID, previous.Manifest.Version)
		}
		if newVersion.Major() != oldVersion.Major() {
			return manifest.Package{}, fmt.Errorf("major upgrade %q from %s to %s requires a future force-upgrade workflow", candidate.Manifest.ID, previous.Manifest.Version, candidate.Manifest.Version)
		}
		if err := validateMigrationUpgrade(previous.MigrationsPath, candidate.MigrationsPath); err != nil {
			return manifest.Package{}, err
		}
	}
	expected := make([]manifest.Package, 0, len(installed)+1)
	for _, pkg := range installed {
		if pkg.Manifest.ID != candidate.Manifest.ID {
			expected = append(expected, pkg)
		}
	}
	expected = append(expected, candidate)
	available, err := i.availableDependencies(ctx)
	if err != nil {
		return manifest.Package{}, err
	}
	if _, err := pluginbuild.RenderBackendWithDependencies(expected, available); err != nil {
		return manifest.Package{}, err
	}
	if _, err := pluginbuild.RenderFrontendWithDependencies(expected, available); err != nil {
		return manifest.Package{}, err
	}

	staged, cleanup, err := i.stage(candidate)
	if err != nil {
		return manifest.Package{}, err
	}
	defer cleanup()
	stagedPkg, err := manifest.Load(staged.backend, i.options.CoreVersion, i.options.SDKMajor)
	if err != nil {
		return manifest.Package{}, fmt.Errorf("validate staged plugin: %w", err)
	}

	before, err := i.applyMigrations(ctx, stagedPkg)
	if err != nil {
		return manifest.Package{}, err
	}
	rollbackMigrations := func() error {
		if i.options.DB == nil {
			return nil
		}
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		return migration.WithPluginMigrations(rollbackCtx, i.options.DB, stagedPkg.Manifest.ID, stagedPkg.MigrationsPath, func(runner *migration.Runner) error {
			_, err := runner.DownTo(rollbackCtx, before)
			return err
		})
	}

	finalBackend := filepath.Join(i.options.Layout.backendInstalledRoot(), installedDir(stagedPkg.Manifest.ID))
	finalFrontend := filepath.Join(i.options.Layout.frontendInstalledRoot(), installedDir(stagedPkg.Manifest.ID))
	buildSnapshot, err := snapshotFiles(i.options.Layout.registryBackendPath(), i.options.Layout.registryFrontendPath(), i.options.Layout.goModPath(), i.options.Layout.goSumPath())
	if err != nil {
		return manifest.Package{}, errors.Join(err, rollbackMigrations())
	}
	oldBackend, oldFrontend, err := i.replaceDirectories(staged, finalBackend, finalFrontend)
	if err != nil {
		return manifest.Package{}, errors.Join(err, rollbackMigrations())
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		returnErr = errors.Join(
			returnErr,
			restoreFiles(buildSnapshot),
			i.restoreDirectory(finalBackend, oldBackend),
			i.restoreDirectory(finalFrontend, oldFrontend),
			rollbackMigrations(),
		)
	}()

	current, err := i.Installed()
	if err != nil {
		return manifest.Package{}, err
	}
	known := append(append([]manifest.Package(nil), current...), installed...)
	if err := i.writeBuildInputs(current, known, available); err != nil {
		return manifest.Package{}, err
	}
	if i.options.Rebuild != nil {
		if err := i.options.Rebuild(ctx, i.options.Layout); err != nil {
			return manifest.Package{}, fmt.Errorf("rebuild application: %w", err)
		}
	}
	if i.options.DB != nil {
		if err := recordInstallation(ctx, i.options.DB, stagedPkg); err != nil {
			return manifest.Package{}, err
		}
	}
	_ = os.RemoveAll(oldBackend)
	_ = os.RemoveAll(oldFrontend)
	committed = true
	return stagedPkg, nil
}

func (i Installer) Uninstall(ctx context.Context, pluginID string) (returnErr error) {
	installed, err := i.Installed()
	if err != nil {
		return err
	}
	var target *manifest.Package
	for index := range installed {
		if installed[index].Manifest.ID == pluginID {
			target = &installed[index]
			break
		}
	}
	if target == nil {
		builtin, err := i.builtinStatus(ctx, pluginID)
		if err != nil {
			return err
		}
		if builtin == "installed" {
			dependents, err := i.builtinDependents(ctx, pluginID)
			if err != nil {
				return err
			}
			for _, plugin := range installed {
				if _, depends := plugin.Manifest.RequiresPlugins[pluginID]; depends {
					dependents = append(dependents, plugin.Manifest.ID)
				}
			}
			if len(dependents) != 0 {
				return fmt.Errorf("plugin %q is required by %s", pluginID, strings.Join(dependents, ", "))
			}
			return markUninstalled(ctx, i.options.DB, pluginID)
		}
		return fmt.Errorf("plugin %q is not installed", pluginID)
	}
	for _, plugin := range installed {
		if _, depends := plugin.Manifest.RequiresPlugins[pluginID]; depends {
			return fmt.Errorf("plugin %q is required by %q", pluginID, plugin.Manifest.ID)
		}
	}
	if i.options.DB != nil {
		refs, err := pluginReferences(ctx, i.options.DB, pluginID, true)
		if err != nil {
			return err
		}
		if len(refs) != 0 {
			return fmt.Errorf("plugin %q has active references: %s", pluginID, strings.Join(refs, ", "))
		}
	}
	backendPath := filepath.Join(i.options.Layout.backendInstalledRoot(), installedDir(pluginID))
	frontendPath := filepath.Join(i.options.Layout.frontendInstalledRoot(), installedDir(pluginID))
	backupBackend := backendPath + ".uninstall-backup"
	backupFrontend := frontendPath + ".uninstall-backup"
	buildSnapshot, err := snapshotFiles(i.options.Layout.registryBackendPath(), i.options.Layout.registryFrontendPath(), i.options.Layout.goModPath(), i.options.Layout.goSumPath())
	if err != nil {
		return err
	}
	_ = os.RemoveAll(backupBackend)
	_ = os.RemoveAll(backupFrontend)
	if err := os.Rename(backendPath, backupBackend); err != nil {
		return fmt.Errorf("stage plugin uninstall: %w", err)
	}
	if err := os.Rename(frontendPath, backupFrontend); err != nil {
		_ = os.Rename(backupBackend, backendPath)
		return fmt.Errorf("stage frontend uninstall: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			_ = os.RemoveAll(backupBackend)
			_ = os.RemoveAll(backupFrontend)
			return
		}
		returnErr = errors.Join(
			returnErr,
			restoreFiles(buildSnapshot),
			restoreRenamedDirectory(backendPath, backupBackend),
			restoreRenamedDirectory(frontendPath, backupFrontend),
		)
	}()
	remaining, err := i.Installed()
	if err != nil {
		return err
	}
	available, err := i.availableDependencies(ctx)
	if err != nil {
		return err
	}
	delete(available, pluginID)
	if err := i.writeBuildInputs(remaining, installed, available); err != nil {
		return err
	}
	if i.options.Rebuild != nil {
		if err := i.options.Rebuild(ctx, i.options.Layout); err != nil {
			return fmt.Errorf("rebuild application: %w", err)
		}
	}
	if i.options.DB != nil {
		if err := markUninstalled(ctx, i.options.DB, pluginID); err != nil {
			return err
		}
	}
	committed = true
	return nil
}

func (i Installer) builtinDependents(ctx context.Context, pluginID string) ([]string, error) {
	if i.options.DB == nil {
		return nil, nil
	}
	rows, err := i.options.DB.QueryContext(ctx, `SELECT plugin_id FROM plugin_installations WHERE source_path = 'builtin' AND status = 'installed' AND plugin_id <> $1 ORDER BY plugin_id`, pluginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var dependents []string
	for rows.Next() {
		var candidate string
		if err := rows.Scan(&candidate); err != nil {
			return nil, err
		}
		if _, depends := version.BuiltinPluginDependencies[candidate][pluginID]; depends {
			dependents = append(dependents, candidate)
		}
	}
	return dependents, rows.Err()
}

func (i Installer) PurgeData(ctx context.Context, pluginID, confirmation string) error {
	if confirmation != "PURGE "+pluginID {
		return errors.New("purge requires --confirm 'PURGE <plugin-id>'")
	}
	if i.options.DB == nil {
		return errors.New("purge-data requires a database connection")
	}
	if status, err := i.builtinStatus(ctx, pluginID); err != nil {
		return err
	} else if status != "" {
		return errors.New("built-in plugin data is owned by core migrations and cannot be purged")
	}
	if _, err := os.Stat(filepath.Join(i.options.Layout.backendInstalledRoot(), installedDir(pluginID))); err == nil {
		return errors.New("uninstall the plugin before purge-data")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tx, err := i.options.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM plugin_installations WHERE plugin_id = $1 FOR UPDATE`, pluginID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("plugin %q has no installation record", pluginID)
		}
		return err
	}
	if status != "uninstalled" {
		return errors.New("uninstall the plugin before purge-data")
	}
	refs, err := pluginReferences(ctx, tx, pluginID, false)
	if err != nil {
		return err
	}
	if len(refs) != 0 {
		return fmt.Errorf("plugin %q has references: %s", pluginID, strings.Join(refs, ", "))
	}
	schema, err := migration.PluginSchemaName(pluginID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
		return fmt.Errorf("drop plugin schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_installations WHERE plugin_id = $1`, pluginID); err != nil {
		return err
	}
	return tx.Commit()
}

func (i Installer) EnableBuiltin(ctx context.Context, pluginID string) (bool, error) {
	status, err := i.builtinStatus(ctx, pluginID)
	if err != nil || status == "" {
		return false, err
	}
	if status == "installed" {
		return true, fmt.Errorf("plugin %q is already installed", pluginID)
	}
	available, err := i.availableDependencies(ctx)
	if err != nil {
		return true, err
	}
	for requiredID, constraintText := range version.BuiltinPluginDependencies[pluginID] {
		requiredVersion, exists := available[requiredID]
		constraint, constraintErr := semver.NewConstraint(constraintText)
		parsed, versionErr := semver.StrictNewVersion(requiredVersion)
		if !exists || constraintErr != nil || versionErr != nil || !constraint.Check(parsed) {
			return true, fmt.Errorf("plugin %q requires installed plugin %q version %s", pluginID, requiredID, constraintText)
		}
	}
	_, err = i.options.DB.ExecContext(ctx, `UPDATE plugin_installations SET status = 'installed', updated_at = CURRENT_TIMESTAMP WHERE plugin_id = $1 AND source_path = 'builtin'`, pluginID)
	return true, err
}

func (i Installer) builtinStatus(ctx context.Context, pluginID string) (string, error) {
	if i.options.DB == nil {
		return "", nil
	}
	var status string
	err := i.options.DB.QueryRowContext(ctx, `SELECT status FROM plugin_installations WHERE plugin_id = $1 AND source_path = 'builtin'`, pluginID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return status, err
}

type stagedPlugin struct {
	backend  string
	frontend string
}

func (i Installer) stage(pkg manifest.Package) (stagedPlugin, func(), error) {
	parent := filepath.Dir(i.options.Layout.backendInstalledRoot())
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return stagedPlugin{}, func() {}, err
	}
	root, err := os.MkdirTemp(parent, ".plugin-stage-")
	if err != nil {
		return stagedPlugin{}, func() {}, err
	}
	backend := filepath.Join(root, "backend")
	frontend := filepath.Join(root, "frontend")
	if err := os.CopyFS(backend, os.DirFS(pkg.Root)); err != nil {
		_ = os.RemoveAll(root)
		return stagedPlugin{}, func() {}, fmt.Errorf("copy plugin source: %w", err)
	}
	frontendRelative, err := filepath.Rel(pkg.Root, filepath.Dir(pkg.FrontendPath))
	if err != nil {
		_ = os.RemoveAll(root)
		return stagedPlugin{}, func() {}, fmt.Errorf("resolve plugin frontend: %w", err)
	}
	if err := os.CopyFS(filepath.Join(frontend, frontendRelative), os.DirFS(filepath.Dir(pkg.FrontendPath))); err != nil {
		_ = os.RemoveAll(root)
		return stagedPlugin{}, func() {}, fmt.Errorf("copy plugin frontend: %w", err)
	}
	return stagedPlugin{backend: backend, frontend: frontend}, func() { _ = os.RemoveAll(root) }, nil
}

func (i Installer) replaceDirectories(staged stagedPlugin, backend, frontend string) (string, string, error) {
	if err := os.MkdirAll(filepath.Dir(backend), 0o755); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(frontend), 0o755); err != nil {
		return "", "", err
	}
	oldBackend, oldFrontend := backend+".old", frontend+".old"
	_ = os.RemoveAll(oldBackend)
	_ = os.RemoveAll(oldFrontend)
	if _, err := os.Stat(backend); err == nil {
		if err := os.Rename(backend, oldBackend); err != nil {
			return "", "", err
		}
	}
	if _, err := os.Stat(frontend); err == nil {
		if err := os.Rename(frontend, oldFrontend); err != nil {
			_ = os.Rename(oldBackend, backend)
			return "", "", err
		}
	}
	if err := os.Rename(staged.backend, backend); err != nil {
		_ = os.Rename(oldBackend, backend)
		_ = os.Rename(oldFrontend, frontend)
		return "", "", err
	}
	if err := os.Rename(staged.frontend, frontend); err != nil {
		_ = os.RemoveAll(backend)
		_ = os.Rename(oldBackend, backend)
		_ = os.Rename(oldFrontend, frontend)
		return "", "", err
	}
	return oldBackend, oldFrontend, nil
}

func (i Installer) restoreDirectory(current, backup string) error {
	if _, err := os.Stat(backup); err == nil {
		_ = os.RemoveAll(current)
		return os.Rename(backup, current)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.RemoveAll(current)
}

func restoreRenamedDirectory(current, backup string) error {
	if _, err := os.Stat(backup); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.RemoveAll(current); err != nil {
		return err
	}
	return os.Rename(backup, current)
}

func (i Installer) applyMigrations(ctx context.Context, pkg manifest.Package) (int64, error) {
	if i.options.DB == nil {
		return 0, nil
	}
	var before int64
	err := migration.WithPluginMigrations(ctx, i.options.DB, pkg.Manifest.ID, pkg.MigrationsPath, func(runner *migration.Runner) error {
		var err error
		before, _, err = runner.Versions(ctx)
		if err != nil {
			return err
		}
		if _, err = runner.Up(ctx, 0); err == nil {
			return nil
		}
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_, rollbackErr := runner.DownTo(rollbackCtx, before)
		return errors.Join(err, rollbackErr)
	})
	if err != nil {
		return 0, fmt.Errorf("apply plugin migrations: %w", err)
	}
	return before, nil
}

func (i Installer) writeBuildInputs(packages, known []manifest.Package, available map[string]string) error {
	backend, err := pluginbuild.RenderBackendWithDependencies(packages, available)
	if err != nil {
		return err
	}
	frontend, err := pluginbuild.RenderFrontendWithDependencies(packages, available)
	if err != nil {
		return err
	}
	if err := replaceFile(i.options.Layout.registryBackendPath(), backend); err != nil {
		return err
	}
	if err := replaceFile(i.options.Layout.registryFrontendPath(), frontend); err != nil {
		return err
	}
	return updateGoMod(i.options.Layout.goModPath(), packages, known)
}

func (i Installer) availableDependencies(ctx context.Context) (map[string]string, error) {
	available := make(map[string]string)
	if i.options.DB == nil {
		for id, pluginVersion := range version.BuiltinPlugins {
			available[id] = pluginVersion
		}
		return available, nil
	}
	rows, err := i.options.DB.QueryContext(ctx, `SELECT plugin_id, version FROM plugin_installations WHERE status = 'installed'`)
	if err != nil {
		return nil, fmt.Errorf("list available plugin dependencies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, pluginVersion string
		if err := rows.Scan(&id, &pluginVersion); err != nil {
			return nil, err
		}
		available[id] = pluginVersion
	}
	return available, rows.Err()
}

func updateGoMod(path string, packages, known []manifest.Package) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read go.mod: %w", err)
	}
	file, err := modfile.Parse(path, raw, nil)
	if err != nil {
		return fmt.Errorf("parse go.mod: %w", err)
	}
	for _, replacement := range append([]manifest.Package(nil), known...) {
		_ = file.DropReplace(replacement.Manifest.Backend.Module, "")
		_ = file.DropRequire(replacement.Manifest.Backend.Module)
	}
	for _, pkg := range packages {
		if err := file.AddRequire(pkg.Manifest.Backend.Module, "v0.0.0"); err != nil {
			return fmt.Errorf("add plugin module %s: %w", pkg.Manifest.Backend.Module, err)
		}
		dir := "./plugin/installed/" + installedDir(pkg.Manifest.ID) + "/backend"
		if err := file.AddReplace(pkg.Manifest.Backend.Module, "", dir, ""); err != nil {
			return fmt.Errorf("add plugin replacement %s: %w", pkg.Manifest.Backend.Module, err)
		}
	}
	formatted, err := file.Format()
	if err != nil {
		return fmt.Errorf("format go.mod: %w", err)
	}
	return replaceFile(path, formatted)
}

func validateMigrationUpgrade(previous, candidate string) error {
	oldEntries, err := os.ReadDir(previous)
	if err != nil {
		return err
	}
	var latest int64
	oldFiles := make(map[string]bool)
	for _, entry := range oldEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, _ := goose.NumericComponent(entry.Name())
		if version > latest {
			latest = version
		}
		oldFiles[entry.Name()] = true
		oldContent, err := os.ReadFile(filepath.Join(previous, entry.Name()))
		if err != nil {
			return err
		}
		newContent, err := os.ReadFile(filepath.Join(candidate, entry.Name()))
		if err != nil || !bytes.Equal(newContent, oldContent) {
			return fmt.Errorf("upgrade must preserve migration %s", entry.Name())
		}
	}
	newEntries, err := os.ReadDir(candidate)
	if err != nil {
		return err
	}
	for _, entry := range newEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") || oldFiles[entry.Name()] {
			continue
		}
		version, _ := goose.NumericComponent(entry.Name())
		if version <= latest {
			return fmt.Errorf("new migration %s must follow installed version %d", entry.Name(), latest)
		}
	}
	return nil
}

func replaceFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".replace-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	backup := path + ".replace-backup"
	_ = os.Remove(backup)
	if _, err := os.Stat(path); err == nil {
		if err := os.Rename(path, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(temporaryName, path); err != nil {
		_ = os.Rename(backup, path)
		return err
	}
	if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

type fileSnapshot struct {
	path    string
	content []byte
	exists  bool
}

func snapshotFiles(paths ...string) ([]fileSnapshot, error) {
	result := make([]fileSnapshot, 0, len(paths))
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			result = append(result, fileSnapshot{path: path})
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, fileSnapshot{path: path, content: content, exists: true})
	}
	return result, nil
}

func restoreFiles(snapshots []fileSnapshot) error {
	var first error
	for _, snapshot := range snapshots {
		var err error
		if snapshot.exists {
			err = replaceFile(snapshot.path, snapshot.content)
		} else {
			err = os.Remove(snapshot.path)
			if errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

func installedDir(pluginID string) string {
	return strings.NewReplacer(".", "_", "-", "_").Replace(pluginID)
}

func recordInstallation(ctx context.Context, db *sql.DB, pkg manifest.Package) error {
	schema, err := migration.PluginSchemaName(pkg.Manifest.ID)
	if err != nil {
		return err
	}
	source := "installed/" + installedDir(pkg.Manifest.ID)
	_, err = db.ExecContext(ctx, `
INSERT INTO plugin_installations (plugin_id, version, schema_name, source_path, status)
VALUES ($1, $2, $3, $4, 'installed')
ON CONFLICT (plugin_id) DO UPDATE SET version = EXCLUDED.version, schema_name = EXCLUDED.schema_name,
source_path = EXCLUDED.source_path, status = 'installed', updated_at = CURRENT_TIMESTAMP`,
		pkg.Manifest.ID, pkg.Manifest.Version, schema, source)
	return err
}

func markUninstalled(ctx context.Context, db *sql.DB, pluginID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM plugin_installations WHERE plugin_id = $1 FOR UPDATE`, pluginID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("plugin %q has no installation record", pluginID)
		}
		return err
	}
	if status != "installed" {
		return fmt.Errorf("plugin %q installation status is %s", pluginID, status)
	}
	references, err := pluginReferences(ctx, tx, pluginID, true)
	if err != nil {
		return err
	}
	if len(references) != 0 {
		return fmt.Errorf("plugin %q has active references: %s", pluginID, strings.Join(references, ", "))
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plugin_installations SET status = 'uninstalled', updated_at = CURRENT_TIMESTAMP WHERE plugin_id = $1`, pluginID); err != nil {
		return err
	}
	return tx.Commit()
}

type referenceQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func pluginReferences(ctx context.Context, db referenceQuerier, pluginID string, activeOnly bool) ([]string, error) {
	query := `SELECT reference_type || ':' || reference_id FROM plugin_references WHERE plugin_id = $1`
	if activeOnly {
		query += ` AND active`
	}
	query += ` ORDER BY reference_type, reference_id`
	rows, err := db.QueryContext(ctx, query, pluginID)
	if err != nil {
		return nil, fmt.Errorf("list plugin references: %w", err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}
