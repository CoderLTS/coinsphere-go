// Command coinsphere provides compile-time plugin tooling.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"coinsphere/backend/internal/pluginbuild"
	"coinsphere/backend/plugin/manifest"
	"coinsphere/backend/version"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "coinsphere: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	if len(args) < 2 || args[0] != "plugin" || args[1] != "validate" {
		return errors.New("usage: coinsphere plugin validate <dir> [<dir>...]")
	}
	if len(args) == 2 {
		return errors.New("at least one plugin directory is required")
	}
	plugins, err := manifest.LoadAll(args[2:], version.Core, version.SDKMajor)
	if err != nil {
		return err
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
