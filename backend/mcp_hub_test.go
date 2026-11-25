package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"textus/backend/ai/aicompat"
	"textus/backend/mcp"
	"textus/backend/programs"
)

func mcpApp(t *testing.T) *App {
	t.Helper()
	a := &App{dirs: Dirs{Config: t.TempDir(), Data: t.TempDir(), Cache: t.TempDir()}}
	a.stores = loadStores(a.dirs)
	t.Cleanup(a.stopMCP)
	return a
}

func TestAServersSecretsGoToTheKeyStoreBeforeAnyWindowHearsThem(t *testing.T) {
	a := mcpApp(t)
	patch := `{"patch":{"mcpServers":[{"id":"gh","command":"npx","env":{"TOKEN":"s3cret"},"headers":{}}]}}`
	if _, err := run(a, "settings_patch", patch); err != nil {
		t.Fatal(err)
	}
	saved := string(a.readSetting(mcp.ServersKey))
	if strings.Contains(saved, "s3cret") || !strings.Contains(saved, mcp.Mask) {
		t.Fatalf("settings hold %s", saved)
	}
	if kept := a.mcpKept(); kept["gh"]["env"]["TOKEN"] != "s3cret" {
		t.Fatalf("kept %v", kept)
	}
	if configs := a.mcpConfigs(); len(configs) != 1 || configs[0].Env["TOKEN"] != "s3cret" {
		t.Fatalf("configs %+v", configs)
	}
	// One an older build left in the settings goes at startup.
	a.stores.settings.patch(jsonObject{mcp.ServersKey: mustJSON([]map[string]any{{"id": "old", "headers": map[string]string{"Authorization": "Bearer x"}}})})
	a.sealSavedMCP()
	if saved := string(a.readSetting(mcp.ServersKey)); strings.Contains(saved, "Bearer x") {
		t.Fatalf("settings hold %s", saved)
	}
	if kept := a.mcpKept(); kept["old"]["headers"]["Authorization"] != "Bearer x" || kept["gh"] != nil {
		t.Fatalf("kept %v", kept)
	}
}

const echoMCPServer = `
const rl = require("readline").createInterface({ input: process.stdin });
const send = (m) => process.stdout.write(JSON.stringify(m) + "\n");
rl.on("line", (line) => {
  const m = JSON.parse(line);
  if (m.method === "initialize")
    send({ jsonrpc: "2.0", id: m.id, result: { serverInfo: { name: "echo", version: "1.0" } } });
  else if (m.method === "tools/list")
    send({ jsonrpc: "2.0", id: m.id, result: { tools: [{ name: "echo", inputSchema: { type: "object" } }] } });
  else if (m.method === "tools/call")
    send({ jsonrpc: "2.0", id: m.id, result: { content: [{ type: "text", text: "echo: " + m.params.arguments.text + " " + process.env.TOKEN }] } });
});
`

func TestAServerTurnedOnOffersItsToolsToARequest(t *testing.T) {
	if _, ok := programs.Find("node", programs.LoginPath()); !ok {
		t.Skip("no node")
	}
	a := mcpApp(t)
	script := filepath.Join(t.TempDir(), "echo.js")
	writeText(t, script, echoMCPServer)
	server := map[string]any{"id": "echo", "name": "Echo", "enabled": true, "command": "node", "args": []string{script}, "env": map[string]string{"TOKEN": "s3cret"}}
	if _, err := run(a, "settings_patch", `{"patch":{"mcpServers":`+string(mustJSON([]any{server}))+`}}`); err != nil {
		t.Fatal(err)
	}
	tools := a.installedTools(context.Background(), "")
	if len(tools) != 1 || tools[0].Spec.Name != "mcp_echo_echo" || !strings.Contains(tools[0].Spec.Description, "MCP server Echo") {
		t.Fatalf("tools %+v", tools)
	}
	if params := tools[0].Spec.Parameters.(map[string]any); params["properties"] == nil {
		t.Errorf("parameters %v", params)
	}
	// The secret reached the server as itself.
	out := tools[0].Run(nil, aicompat.ToolCall{CallID: "c", Name: "mcp_echo_echo", Arguments: `{"text":"hi"}`})
	if out.Text != "echo: hi s3cret" {
		t.Fatalf("%q", out.Text)
	}
	status, err := run(a, "mcp_status", `{}`)
	if err != nil || !strings.Contains(status, `"state":"ready"`) || !strings.Contains(status, `"server":"echo 1.0"`) {
		t.Fatalf("%s %v", status, err)
	}
	running := a.mcp.running["echo"]
	if restarted, err := run(a, "mcp_restart", `{"id":"echo"}`); err != nil || !strings.Contains(restarted, `"id":"echo"`) {
		t.Fatalf("%s %v", restarted, err)
	}
	if a.mcp.running["echo"] == running {
		t.Fatal("not restarted")
	}
	select {
	case <-running.conn.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the old server still runs")
	}
	// Off: stopped and offered no more.
	server["enabled"] = false
	if _, err := run(a, "settings_patch", `{"patch":{"mcpServers":`+string(mustJSON([]any{server}))+`}}`); err != nil {
		t.Fatal(err)
	}
	if status, err := run(a, "mcp_status", `{}`); err != nil || status != `[]` {
		t.Fatalf("%s %v", status, err)
	}
}

func TestTryingAServerNeedsACommandOrAURL(t *testing.T) {
	a := mcpApp(t)
	if _, err := run(a, "mcp_try", `{"server":{"id":"x","command":"  "}}`); err == nil || err.Error() != "Give a command or a URL." {
		t.Fatalf("%v", err)
	}
	if _, err := run(a, "mcp_try", `{"server":{"id":"x","command":"textus-no-such-server"}}`); err == nil || !strings.Contains(err.Error(), "isn't installed") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(a.keysPath()); err == nil {
		t.Error("trying wrote the key store")
	}
}
