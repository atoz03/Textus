package backend

import (
	"encoding/json/v2"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"textus/backend/plugins"
	"textus/backend/programs"
)

func TestALongStepReachesTheWindowWithoutWhatItNeverReads(t *testing.T) {
	long := strings.Repeat("x", 9000)
	line := string(mustJSON(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"update": map[string]any{
		"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "completed",
		"rawInput":  map[string]any{"content": long, "file_path": "a.md", "command": long},
		"rawOutput": long,
		"content": []any{
			map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": long}},
			map[string]any{"type": "diff", "path": "a.md", "oldText": long, "newText": long},
		},
	}}}))
	var slimmed struct {
		Params struct {
			Update map[string]any `json:"update"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(slim(line)), &slimmed); err != nil {
		t.Fatal(err)
	}
	update := slimmed.Params.Update
	if _, ok := update["rawOutput"]; ok {
		t.Error("rawOutput kept")
	}
	// Of its input, what says what it does, cut short.
	input := update["rawInput"].(map[string]any)
	if input["file_path"] != "a.md" || input["content"] != nil || len(input["command"].(string)) != stepDetail {
		t.Errorf("rawInput %v", len(input["command"].(string)))
	}
	content := update["content"].([]any)
	text := content[0].(map[string]any)["content"].(map[string]any)["text"].(string)
	if len(content) != 1 || len(text) != stepText || update["toolCallId"] != "t1" {
		t.Errorf("content %d %d", len(content), len(text))
	}
	// A permission request keeps its step whole: the prompt shows it.
	ask := string(mustJSON(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "session/request_permission",
		"params": map[string]any{"toolCall": map[string]any{"rawInput": map[string]any{"command": long}}}}))
	if slim(ask) != ask {
		t.Error("a permission request was cut")
	}
	if short := `{"method":"session/update"}`; slim(short) != short {
		t.Error("a short line was changed")
	}
}

func TestAnAgentStartsOnlyAsItsManifestNamesIt(t *testing.T) {
	manifest := mustJSON(map[string]any{"contributes": map[string]any{"agents": []any{
		map[string]any{"id": "fake", "command": "node", "args": []any{"{pluginDir}/agent.js", "--acp", 3},
			"env": map[string]any{"HOME_HINT": "{pluginDir}"}},
		map[string]any{"id": "empty", "command": " "},
	}}})
	launch, err := launchOf(manifest, "fake", "/p")
	if err != nil || launch.command != "node" || !slices.Equal(launch.args, []string{"/p/agent.js", "--acp"}) ||
		!slices.Equal(launch.env, [][2]string{{"HOME_HINT", "/p"}}) {
		t.Fatalf("%+v %v", launch, err)
	}
	if _, err := launchOf(manifest, "empty", "/p"); err == nil || err.Error() != "An agent needs a command." {
		t.Errorf("%v", err)
	}
	if _, err := launchOf(manifest, "missing", "/p"); err == nil || err.Error() != "No agent missing." {
		t.Errorf("%v", err)
	}
}

func TestAnAgentsLinesGoToTheWindowGatheredThenItsEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh")
	}
	p, err := programs.Start("sh", []string{"-c", `printf 'one\n\ntwo\r\n'; echo bye >&2; exit 3`}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var sent []agentOutput
	ended := false
	relayAgent(p, func(out agentOutput) error {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, out)
		return nil
	}, func() { ended = true })
	var lines []string
	for _, out := range sent[:len(sent)-1] {
		lines = append(lines, out["lines"].([]string)...)
	}
	last := sent[len(sent)-1]
	if !slices.Equal(lines, []string{"one", "two"}) || !ended {
		t.Fatalf("%v %v", lines, ended)
	}
	if got := string(mustJSON(last)); got != `{"code":3,"stderr":"bye\n","type":"exit"}` {
		t.Fatalf("%s", got)
	}
}

func TestOnlyAPluginGrantedAgentsStartsOne(t *testing.T) {
	a := pluginApp(t)
	a.defaults = map[string]bool{"textusAi": true}
	a.stores = loadStores(a.dirs)
	if err := a.updatePlugins(func(s *plugins.State) {
		s.Entry("a.b").Enabled = true
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := run(a, "agent_start", `{"plugin":"a.b","agent":"x","cwd":null}`); err == nil || !strings.Contains(err.Error(), "was not granted agents") {
		t.Fatalf("%v", err)
	}
	if _, err := run(a, "agent_send", `{"handle":7,"line":"{}"}`); err == nil || err.Error() != "The agent has stopped." {
		t.Fatalf("%v", err)
	}
	if _, err := run(a, "agent_stop", `{"handle":7}`); err != nil {
		t.Fatal(err)
	}
}
