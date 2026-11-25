package mcp

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"textus/backend/programs"
)

// echoServer is a server in a few lines of Node: initialize, one tool that
// echoes on pages whose cursors go round (a, b, a…), a ping of its own
// before answering tools/call.
const echoServer = `
const rl = require("readline").createInterface({ input: process.stdin });
const send = (m) => process.stdout.write(JSON.stringify(m) + "\n");
rl.on("line", (line) => {
  const m = JSON.parse(line);
  if (m.method === "initialize")
send({ jsonrpc: "2.0", id: m.id, result: { protocolVersion: "2025-06-18", capabilities: { tools: {} }, serverInfo: { name: "echo", version: "1.0" } } });
  else if (m.method === "tools/list")
send({ jsonrpc: "2.0", id: m.id, result: { nextCursor: m.params?.cursor === "a" ? "b" : "a", tools: [{ name: "echo", description: "Says it back.", inputSchema: { type: "object", properties: { text: { type: "string" } } } }] } });
  else if (m.method === "tools/call") {
send({ jsonrpc: "2.0", id: 900, method: "ping" });
send({ jsonrpc: "2.0", id: m.id, result: { content: [{ type: "text", text: "echo: " + m.params.arguments.text }] } });
  } else if (m.id === 900) process.stderr.write("pong\n");
});
`

// EchoServer is the echo server's script written in dir, with node to run
// it; false where there is no node.
func EchoServer(t *testing.T) (Endpoint, bool) {
	t.Helper()
	if _, ok := programs.Find("node", programs.LoginPath()); !ok {
		return Endpoint{}, false
	}
	script := filepath.Join(t.TempDir(), "echo.js")
	if err := os.WriteFile(script, []byte(echoServer), 0o644); err != nil {
		t.Fatal(err)
	}
	return Endpoint{Program: "node", Args: []string{script}}, true
}

func TestAServerIsStartedAskedForItsToolsAndCalled(t *testing.T) {
	e, ok := EchoServer(t)
	if !ok {
		t.Skip("no node")
	}
	ctx := context.Background()
	conn, err := Open(ctx, e, http.DefaultClient, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if conn.Server != "echo 1.0" {
		t.Errorf("server %q", conn.Server)
	}
	tools, err := conn.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Pages none, a, b; then a again would go round for ever.
	if len(tools) != 3 || tools[0].Name != "echo" || tools[0].Description != "Says it back." {
		t.Fatalf("tools %+v", tools)
	}
	text, failed, err := conn.CallTool(ctx, "echo", jsontext.Value(`{"text":"hi"}`))
	if err != nil || failed || text != "echo: hi" {
		t.Fatalf("%q %v %v", text, failed, err)
	}
	if !conn.Alive() {
		t.Error("the server stopped")
	}
	// It answered the ping its server sent: the server says so.
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(conn.stdio.p.Stderr.String(), "pong"); {
		if time.Now().After(deadline) {
			t.Fatalf("no pong: %q", conn.stdio.p.Stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	conn.Close()
	<-conn.stdio.gone
	if _, _, err := conn.CallTool(ctx, "echo", jsontext.Value(`{}`)); err == nil || !strings.Contains(err.Error(), "stopped. It said: pong") {
		t.Fatalf("a call to a stopped server: %v", err)
	}
	<-conn.stdio.p.Done()
	if conn.Alive() {
		t.Error("a stopped server is alive")
	}
}

func TestACommandNotInstalledSaysSo(t *testing.T) {
	_, err := Open(context.Background(), Endpoint{Program: "textus-no-such-server"}, http.DefaultClient, "1")
	if err == nil || !strings.Contains(err.Error(), "isn't installed") {
		t.Fatalf("%v", err)
	}
}

func TestAServerThatStopsSaysWhatItSaid(t *testing.T) {
	if _, ok := programs.Find("sh", programs.LoginPath()); !ok {
		t.Skip("no sh")
	}
	_, err := Open(context.Background(), Endpoint{Program: "sh", Args: []string{"-c", "echo 'npm ERR! 404' >&2; exit 1"}}, http.DefaultClient, "1")
	if err == nil || !strings.Contains(err.Error(), "the server stopped. It said: npm ERR! 404") {
		t.Fatalf("%v", err)
	}
}

func TestAToolNameIsWhatEveryProviderTakes(t *testing.T) {
	if got := ToolName("File System", "read_file"); got != "mcp_file_system_read_file" {
		t.Errorf("%q", got)
	}
	if got := ToolName("知识库", "search"); got != "mcp_search" {
		t.Errorf("%q", got)
	}
	if got := ToolName(strings.Repeat("x", 80), "y"); len(got) > 64 {
		t.Errorf("%d long", len(got))
	}
	// Two long tools alike in their first 53 characters stay apart and
	// short.
	long := func(end string) string { return ToolName("github", strings.Repeat("a", 60)+end) }
	first := UniqueName(long("1"), nil)
	second := UniqueName(long("2"), []string{first})
	if first == second || len(second) > 64 {
		t.Errorf("%q %q", first, second)
	}
	if got := UniqueName("mcp_a", []string{"mcp_a", "mcp_a_2"}); got != "mcp_a_3" {
		t.Errorf("%q", got)
	}
}

func TestAServerIsACommandOrAURL(t *testing.T) {
	var config Config
	if err := json.Unmarshal([]byte(`{"id":"fs","name":"Files","enabled":true,"command":"npx","args":["-y","server"],"env":{"B":"2","A":"1"}}`), &config); err != nil {
		t.Fatal(err)
	}
	e, ok := config.Endpoint()
	want := Endpoint{Program: "npx", Args: []string{"-y", "server"}, Env: [][2]string{{"A", "1"}, {"B", "2"}}}
	if !ok || !e.Equal(want) {
		t.Fatalf("%+v", e)
	}
	if config.Label() != "Files" {
		t.Errorf("label %q", config.Label())
	}
	web := Config{ID: "w", URL: " https://x.test/mcp "}
	if e, ok := web.Endpoint(); !ok || e.URL != "https://x.test/mcp" || web.Label() != "w" {
		t.Fatalf("%+v", e)
	}
	if _, ok := (Config{ID: "e"}).Endpoint(); ok {
		t.Fatal("a server with neither has an endpoint")
	}
}

func TestATypedValueIsKeptApartAndMaskedAMaskedOneStaysAndAGoneOneGoes(t *testing.T) {
	servers := jsontext.Value(`[{"id":"a","env":{"TOKEN":"s3cret","EMPTY":""},"headers":{}}]`)
	kept, sealed, changed := Seal(Kept{}, servers)
	if !changed || kept["a"]["env"]["TOKEN"] != "s3cret" || len(kept["a"]["env"]) != 1 {
		t.Fatalf("%v %s %v", kept, sealed, changed)
	}
	var list []Config
	if err := json.Unmarshal(sealed, &list); err != nil || list[0].Env["TOKEN"] != Mask || list[0].Env["EMPTY"] != "" {
		t.Fatalf("%s %v", sealed, err)
	}
	// Saved again unchanged: the secret stays; the server removed: it goes.
	again, _, changed := Seal(kept, sealed)
	if changed || again["a"]["env"]["TOKEN"] != "s3cret" {
		t.Fatalf("%v %v", again, changed)
	}
	if gone, _, _ := Seal(kept, jsontext.Value(`[]`)); len(gone) != 0 {
		t.Fatalf("%v", gone)
	}
	// Its name changed: the mask has nothing behind it, so it is emptied.
	_, renamed, _ := Seal(kept, jsontext.Value(`[{"id":"a","env":{"API_TOKEN":"`+Mask+`"}}]`))
	if !strings.Contains(string(renamed), `"API_TOKEN":""`) {
		t.Fatalf("%s", renamed)
	}
	// Started, the mask is the secret again.
	config := Config{ID: "a", Env: map[string]string{"TOKEN": Mask, "OTHER": "x"}, Headers: map[string]string{"H": Mask}}
	Unmask(kept, &config)
	if config.Env["TOKEN"] != "s3cret" || config.Env["OTHER"] != "x" || config.Headers["H"] != Mask {
		t.Fatalf("%+v", config)
	}
}

func TestAWebServerAnswersInJSONOrAnEventStream(t *testing.T) {
	var sessions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessions = append(sessions, r.Header.Get("Mcp-Session-Id"))
		if r.Header.Get("Authorization") != "Bearer t" || r.Header.Get("MCP-Protocol-Version") == "" {
			http.Error(w, "who are you", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var m struct {
			ID     *uint64 `json:"id"`
			Method string  `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &m)
		answer := func(result string) string {
			raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": *m.ID, "result": jsontext.Value(result)})
			return string(raw)
		}
		switch m.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "s1")
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, answer(`{"serverInfo":{"name":"web"}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n")
			io.WriteString(w, "data: "+answer(`{"tools":[{"name":"look","inputSchema":"bad"},{"description":"no name"}]}`)+"\n\n")
		case "tools/call":
			w.Header().Set("Content-Type", "application/json")
			if m.Params.Name == "look" {
				io.WriteString(w, `{"jsonrpc":"2.0","id":`+mustID(m.ID)+`,"error":{"code":1,"message":"no such page"}}`)
				return
			}
			io.WriteString(w, answer(`{"content":[{"type":"text","text":"a"},{"type":"image"},{"type":"audio"},`+
				`{"type":"resource_link","uri":"file:///x"},{"type":"resource","resource":{"uri":"y"}},{"type":"other"}],"isError":true}`))
		}
	}))
	defer server.Close()
	ctx := context.Background()
	if _, err := Open(ctx, Endpoint{URL: server.URL}, server.Client(), "1"); err == nil || !strings.Contains(err.Error(), "401 Unauthorized: who are you") {
		t.Fatalf("without its header: %v", err)
	}
	sessions = nil
	conn, err := Open(ctx, Endpoint{URL: server.URL, Headers: [][2]string{{"Authorization", "Bearer t"}}}, server.Client(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if conn.Server != "web" {
		t.Errorf("server %q", conn.Server)
	}
	tools, err := conn.ListTools(ctx)
	if err != nil || len(tools) != 1 || tools[0].Name != "look" || string(tools[0].InputSchema) != `{"type":"object","properties":{}}` {
		t.Fatalf("%+v %v", tools, err)
	}
	if _, _, err := conn.CallTool(ctx, "look", jsontext.Value(`{}`)); err == nil || err.Error() != "no such page" {
		t.Fatalf("%v", err)
	}
	text, failed, err := conn.CallTool(ctx, "parts", jsontext.Value(`{}`))
	if err != nil || !failed || text != "a\n[a picture]\n[a sound]\n[resource file:///x]\n[resource y]" {
		t.Fatalf("%q %v %v", text, failed, err)
	}
	if !slices.Equal(sessions, []string{"", "s1", "s1", "s1", "s1"}) {
		t.Fatalf("sessions %q", sessions)
	}
}

func mustID(id *uint64) string {
	raw, _ := json.Marshal(*id)
	return string(raw)
}

func TestAnAnswerIsItsResultOrItsErrorsMessage(t *testing.T) {
	reply := answerOf(map[string]jsontext.Value{"result": jsontext.Value(`{"content":[]}`)})
	if reply.err != nil || string(reply.result) != `{"content":[]}` {
		t.Fatalf("%+v", reply)
	}
	if none := answerOf(map[string]jsontext.Value{"error": jsontext.Value(`null`)}); none.err != nil || string(none.result) != "null" {
		t.Fatalf("%+v", none)
	}
	refused := answerOf(map[string]jsontext.Value{"error": jsontext.Value(`{"code":1}`)})
	if refused.err == nil || refused.err.Error() != "the server refused" {
		t.Fatalf("%+v", refused)
	}
}
