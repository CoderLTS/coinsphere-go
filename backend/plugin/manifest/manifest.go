// Package manifest validates CoinSphere compile-time plugin manifests.
package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	semver "github.com/Masterminds/semver/v3"
	"golang.org/x/mod/modfile"
)

const (
	FileName         = "coinsphere-plugin.json"
	SchemaVersion    = 1
	maxManifestBytes = 1 << 20
)

var pluginIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*(?:\.[a-z][a-z0-9]*(?:-[a-z0-9]+)*)+$`)
var windowsAbsolutePathPattern = regexp.MustCompile(`^[A-Za-z]:/`)

var contributionTypes = map[string]bool{
	"nodes": true, "triggers": true, "strategies": true, "apiRoutes": true, "pages": true,
	"resultPages": true, "assistantQueries": true, "migrations": true,
	"marketDataProviders": true, "executionProviders": true, "workflowValidators": true, "templates": true,
}

type Manifest struct {
	SchemaVersion   int               `json:"schemaVersion"`
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Menu            Menu              `json:"menu,omitempty"`
	Version         string            `json:"version"`
	SDKMajor        int               `json:"sdkMajor"`
	RequiresCore    string            `json:"requiresCore"`
	RequiresPlugins map[string]string `json:"requiresPlugins,omitempty"`
	Backend         Backend           `json:"backend"`
	Frontend        Frontend          `json:"frontend"`
	Migrations      Migrations        `json:"migrations"`
	Contributes     []string          `json:"contributes"`
}

type Menu struct {
	Mode   string `json:"mode,omitempty"`
	Title  string `json:"title,omitempty"`
	Icon   string `json:"icon,omitempty"`
	Parent string `json:"parent,omitempty"`
}

type Backend struct {
	Module  string `json:"module"`
	Package string `json:"package"`
}

type Frontend struct {
	Entry string `json:"entry"`
}

type Migrations struct {
	Directory string `json:"directory"`
}

type Package struct {
	Root           string
	Manifest       Manifest
	BackendPath    string
	FrontendPath   string
	MigrationsPath string
}

func Load(root, coreVersion string, sdkMajor int) (Package, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Package{}, fmt.Errorf("resolve plugin root: %w", err)
	}
	info, err := os.Stat(resolvedRoot)
	if err != nil || !info.IsDir() {
		return Package{}, errors.New("plugin root must be an existing directory")
	}

	manifestPath, err := resolveInside(resolvedRoot, FileName, false)
	if err != nil {
		return Package{}, fmt.Errorf("resolve %s: %w", FileName, err)
	}
	file, err := os.Open(manifestPath)
	if err != nil {
		return Package{}, fmt.Errorf("open %s: %w", FileName, err)
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return Package{}, fmt.Errorf("stat %s: %w", FileName, err)
	}
	if info.Size() > maxManifestBytes {
		return Package{}, fmt.Errorf("%s exceeds %d bytes", FileName, maxManifestBytes)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxManifestBytes))
	decoder.DisallowUnknownFields()
	var value Manifest
	if err := decoder.Decode(&value); err != nil {
		return Package{}, fmt.Errorf("decode %s: %w", FileName, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Package{}, err
	}
	if err := Validate(value, coreVersion, sdkMajor); err != nil {
		return Package{}, err
	}

	backendPath, err := resolveInside(resolvedRoot, value.Backend.Package, true)
	if err != nil {
		return Package{}, fmt.Errorf("backend.package: %w", err)
	}
	frontendPath, err := resolveInside(resolvedRoot, value.Frontend.Entry, false)
	if err != nil {
		return Package{}, fmt.Errorf("frontend.entry: %w", err)
	}
	migrationsPath, err := resolveInside(resolvedRoot, value.Migrations.Directory, true)
	if err != nil {
		return Package{}, fmt.Errorf("migrations.directory: %w", err)
	}
	if err := validateGoModule(backendPath, value.Backend.Module); err != nil {
		return Package{}, err
	}
	return Package{
		Root: resolvedRoot, Manifest: value, BackendPath: backendPath,
		FrontendPath: frontendPath, MigrationsPath: migrationsPath,
	}, nil
}

func LoadAll(roots []string, coreVersion string, sdkMajor int) ([]Package, error) {
	return LoadAllWithDependencies(roots, coreVersion, sdkMajor, nil)
}

func LoadAllWithDependencies(roots []string, coreVersion string, sdkMajor int, available map[string]string) ([]Package, error) {
	packages := make([]Package, 0, len(roots))
	seen := make(map[string]string, len(roots))
	for _, root := range roots {
		plugin, err := Load(root, coreVersion, sdkMajor)
		if err != nil {
			return nil, fmt.Errorf("plugin %s: %w", root, err)
		}
		if previous := seen[plugin.Manifest.ID]; previous != "" {
			return nil, fmt.Errorf("duplicate plugin id %q in %s and %s", plugin.Manifest.ID, previous, plugin.Root)
		}
		seen[plugin.Manifest.ID] = plugin.Root
		packages = append(packages, plugin)
	}
	return SortByDependenciesWithAvailable(packages, available)
}

func SortByDependencies(packages []Package) ([]Package, error) {
	return SortByDependenciesWithAvailable(packages, nil)
}

func SortByDependenciesWithAvailable(packages []Package, available map[string]string) ([]Package, error) {
	byID := make(map[string]Package, len(packages))
	for _, plugin := range packages {
		byID[plugin.Manifest.ID] = plugin
	}
	for _, plugin := range packages {
		for requiredID, constraintText := range plugin.Manifest.RequiresPlugins {
			required, exists := byID[requiredID]
			if !exists {
				availableVersion := available[requiredID]
				constraint, _ := semver.NewConstraint(constraintText)
				version, versionErr := semver.StrictNewVersion(availableVersion)
				if availableVersion == "" || versionErr != nil || !constraint.Check(version) {
					return nil, fmt.Errorf("plugin %q requires missing plugin %q", plugin.Manifest.ID, requiredID)
				}
				continue
			}
			constraint, _ := semver.NewConstraint(constraintText)
			version, _ := semver.StrictNewVersion(required.Manifest.Version)
			if !constraint.Check(version) {
				return nil, fmt.Errorf("plugin %q requires plugin %q version %s", plugin.Manifest.ID, requiredID, constraintText)
			}
		}
	}
	result := make([]Package, 0, len(packages))
	state := make(map[string]uint8, len(packages))
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return fmt.Errorf("plugin dependency cycle includes %q", id)
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		plugin := byID[id]
		dependencies := make([]string, 0, len(plugin.Manifest.RequiresPlugins))
		for requiredID := range plugin.Manifest.RequiresPlugins {
			dependencies = append(dependencies, requiredID)
		}
		sort.Strings(dependencies)
		for _, requiredID := range dependencies {
			if _, exists := byID[requiredID]; exists {
				if err := visit(requiredID); err != nil {
					return err
				}
			}
		}
		state[id] = 2
		result = append(result, plugin)
		return nil
	}
	ids := make([]string, 0, len(packages))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func Validate(value Manifest, coreVersion string, sdkMajor int) error {
	if value.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schemaVersion must be %d", SchemaVersion)
	}
	if !pluginIDPattern.MatchString(value.ID) {
		return errors.New("id must be a lowercase dotted name")
	}
	if strings.TrimSpace(value.Name) == "" {
		return errors.New("name is required")
	}
	if value.Menu.Mode != "" && value.Menu.Mode != "own" && value.Menu.Mode != "existing" && value.Menu.Mode != "direct" {
		return fmt.Errorf("menu.mode %q is invalid", value.Menu.Mode)
	}
	if value.Menu.Mode == "existing" && strings.TrimSpace(value.Menu.Parent) == "" {
		return errors.New("menu.parent is required for existing menu mode")
	}
	if value.Menu.Mode != "existing" && strings.TrimSpace(value.Menu.Parent) != "" {
		return errors.New("menu.parent is only valid for existing menu mode")
	}
	if _, err := semver.StrictNewVersion(value.Version); err != nil {
		return fmt.Errorf("version must be strict SemVer: %w", err)
	}
	if value.SDKMajor != sdkMajor {
		return fmt.Errorf("sdkMajor %d is incompatible with supported major %d", value.SDKMajor, sdkMajor)
	}
	core, err := semver.StrictNewVersion(coreVersion)
	if err != nil {
		return fmt.Errorf("invalid core version %q: %w", coreVersion, err)
	}
	constraint, err := semver.NewConstraint(value.RequiresCore)
	if err != nil {
		return fmt.Errorf("requiresCore is invalid: %w", err)
	}
	if !constraint.Check(core) {
		return fmt.Errorf("requiresCore %q does not include core %s", value.RequiresCore, coreVersion)
	}
	for id, constraintText := range value.RequiresPlugins {
		if id == value.ID || !pluginIDPattern.MatchString(id) {
			return fmt.Errorf("requiresPlugins contains invalid plugin id %q", id)
		}
		if _, err := semver.NewConstraint(constraintText); err != nil {
			return fmt.Errorf("requiresPlugins constraint for %q is invalid: %w", id, err)
		}
	}
	if strings.TrimSpace(value.Backend.Module) == "" || strings.TrimSpace(value.Backend.Package) == "" {
		return errors.New("backend.module and backend.package are required")
	}
	if strings.TrimSpace(value.Frontend.Entry) == "" {
		return errors.New("frontend.entry is required")
	}
	if strings.TrimSpace(value.Migrations.Directory) == "" {
		return errors.New("migrations.directory is required")
	}
	if len(value.Contributes) == 0 {
		return errors.New("contributes must not be empty")
	}
	seen := make(map[string]bool, len(value.Contributes))
	for _, contribution := range value.Contributes {
		if !contributionTypes[contribution] {
			return fmt.Errorf("unsupported contribution %q", contribution)
		}
		if seen[contribution] {
			return fmt.Errorf("duplicate contribution %q", contribution)
		}
		seen[contribution] = true
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("manifest must contain exactly one JSON object")
		}
		return fmt.Errorf("decode trailing manifest content: %w", err)
	}
	return nil
}

func resolveInside(root, relative string, wantDirectory bool) (string, error) {
	if strings.Contains(relative, `\`) {
		return "", errors.New("path must use forward slashes")
	}
	cleanSlash := path.Clean(relative)
	if path.IsAbs(relative) || filepath.IsAbs(relative) || windowsAbsolutePathPattern.MatchString(relative) {
		return "", errors.New("absolute paths are not allowed")
	}
	if cleanSlash == "." || cleanSlash == ".." || strings.HasPrefix(cleanSlash, "../") {
		return "", errors.New("path must name a child of the plugin root")
	}
	clean := filepath.FromSlash(cleanSlash)
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, clean))
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes the plugin root")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if info.IsDir() != wantDirectory {
		if wantDirectory {
			return "", errors.New("path must be a directory")
		}
		return "", errors.New("path must be a file")
	}
	return resolved, nil
}

func validateGoModule(backendPath, expected string) error {
	raw, err := os.ReadFile(filepath.Join(backendPath, "go.mod"))
	if err != nil {
		return fmt.Errorf("backend.package must contain go.mod: %w", err)
	}
	parsed, err := modfile.Parse("go.mod", raw, nil)
	if err != nil || parsed.Module == nil {
		return errors.New("backend go.mod is invalid")
	}
	if parsed.Module.Mod.Path != expected {
		return fmt.Errorf("backend.module %q does not match go.mod module %q", expected, parsed.Module.Mod.Path)
	}
	return nil
}
