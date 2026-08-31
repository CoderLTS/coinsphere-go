// Package contracttest exercises a plugin through the same static SDK registry used by CoinSphere.
package contracttest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"coinsphere/backend/plugin/manifest"
	"coinsphere/backend/plugin/sdk"
	"coinsphere/backend/version"
	"github.com/gin-gonic/gin"
)

type Contract struct {
	t        testing.TB
	pkg      manifest.Package
	registry *sdk.Registry
}

func Load(t testing.TB, root string, register sdk.RegisterFunc) *Contract {
	t.Helper()
	pkg, err := manifest.Load(root, version.Core, version.SDKMajor)
	if err != nil {
		t.Fatalf("load plugin manifest: %v", err)
	}
	registry := sdk.NewRegistry()
	if err := registry.RegisterPlugin(sdk.PluginDescriptor{
		ID: pkg.Manifest.ID, Name: pkg.Manifest.Name, Version: pkg.Manifest.Version,
		Contributes: pkg.Manifest.Contributes,
	}, register); err != nil {
		t.Fatalf("register plugin: %v", err)
	}
	return &Contract{t: t, pkg: pkg, registry: registry}
}

func (c *Contract) Execute(ctx context.Context, nodeType string, request sdk.ActionRequest) sdk.ActionResult {
	c.t.Helper()
	_, handler, ok := c.registry.Action(nodeType)
	if !ok {
		c.t.Fatalf("action %q is not registered", nodeType)
	}
	result, err := handler.Execute(ctx, request)
	if err != nil {
		c.t.Fatalf("execute action %q: %v", nodeType, err)
	}
	if len(result.Output) == 0 || !json.Valid(result.Output) {
		c.t.Fatalf("action %q returned invalid JSON", nodeType)
	}
	return result
}

func (c *Contract) ServeRoute(desc sdk.RouteDescriptor, request *http.Request, scope sdk.RouteScope) *httptest.ResponseRecorder {
	c.t.Helper()
	handler, ok := c.registry.Route(c.pkg.Manifest.ID, desc)
	if !ok {
		c.t.Fatalf("route %s %s is not registered", desc.Method, desc.Pattern)
	}
	response := httptest.NewRecorder()
	router := gin.New()
	router.Handle(strings.ToUpper(strings.TrimSpace(desc.Method)), desc.Pattern, func(ctx *gin.Context) {
		handler(ctx, scope)
	})
	router.ServeHTTP(response, request)
	return response
}

func (c *Contract) ResultPage(pageKey string) sdk.ResultPageDescriptor {
	c.t.Helper()
	page, ok := c.registry.ResultPage(c.pkg.Manifest.ID, pageKey)
	if !ok {
		c.t.Fatalf("result page %q is not registered", pageKey)
	}
	entry := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(page.ComponentEntry, "./")))
	if filepath.IsAbs(entry) || entry == ".." || strings.HasPrefix(entry, ".."+string(filepath.Separator)) {
		c.t.Fatalf("result page %q escapes the plugin root", pageKey)
	}
	if info, err := os.Stat(filepath.Join(c.pkg.Root, entry)); err != nil || info.IsDir() {
		c.t.Fatalf("result page %q component is not a file: %v", pageKey, err)
	}
	return page
}
