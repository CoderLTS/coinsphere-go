package pluginlifecycle

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"coinsphere/backend/internal/testdb"
)

func TestInstalledBusinessPluginCompilesAndRegistersAllContributions(t *testing.T) {
	database, _ := testdb.Open(t, true)
	core, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	layout := Layout{BackendRoot: filepath.Join(root, "backend"), FrontendRoot: filepath.Join(root, "frontend")}
	if err := os.MkdirAll(layout.BackendRoot, 0755); err != nil {
		t.Fatal(err)
	}
	module := "module coinsphere/example-host\n\ngo 1.26.6\n\nrequire coinsphere/backend v0.0.0\nreplace coinsphere/backend => " + filepath.ToSlash(core) + "\n"
	if err := os.WriteFile(layout.goModPath(), []byte(module), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	source := filepath.Join(core, "..", "examples", "plugins", "business")
	installer := New(Options{Layout: layout, DB: database})
	if _, err := installer.Install(ctx, source, false); err != nil {
		t.Fatal(err)
	}
	// Compile the package and generated registry exactly as the application
	// does. No production plugin registration is added for this test fixture.
	check := `package pluginregistry
import (
 "context"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "testing"
 "coinsphere/backend/plugin/sdk"
 "github.com/gin-gonic/gin"
)
func TestBusiness(t *testing.T) {
 r:=sdk.NewRegistry()
 if err:=RegisterAll(r,sdk.Host{},map[string]bool{"example.business":true});err!=nil{t.Fatal(err)}
 _,action,ok:=r.Action("example.business.task")
 if !ok {t.Fatal("node missing")}
 result,err:=action.Execute(context.Background(),sdk.ActionRequest{Input:json.RawMessage("{}"),Config:json.RawMessage("{}")})
 if err!=nil || string(result.Output)!="{\"message\":\"业务事项可读取\"}" {t.Fatal("node failed",err)}
 if len(r.RunPanels())!=1 {t.Fatal("panel missing")}
 page,ok:=r.ResultPage("example.business","tasks")
 if !ok || page.ActionPermissions["ack"]=="" {t.Fatal("result action missing")}
 for _,route:=range r.Routes(){
   recorder:=httptest.NewRecorder();c,_:=gin.CreateTestContext(recorder)
   c.Request=httptest.NewRequest(route.Descriptor.Method,"/scoped",nil)
   var scope sdk.RouteScope=sdk.ResultScope{ViewID:"41",PluginID:"example.business"}
   if route.Descriptor.Scope==sdk.ScopeWorkflow {scope=sdk.WorkflowScope{WorkflowID:"7",PluginID:"example.business"}}
   route.Handler(c,scope)
   if recorder.Code!=http.StatusOK {t.Fatal("scoped route failed")}
 }
}`
	if err := os.WriteFile(filepath.Join(filepath.Dir(layout.registryBackendPath()), "business_test.go"), []byte(check), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "go", "test", "-mod=mod", "./internal/pluginregistry")
	cmd.Dir = layout.BackendRoot
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("installed example did not compile/register: %v\n%s", err, output)
	}
}
