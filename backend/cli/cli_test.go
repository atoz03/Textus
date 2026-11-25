package cli

import (
	"encoding/json/v2"
	"net"
	"strings"
	"testing"
)

func TestRepliesAreOneJSONObjectALine(t *testing.T) {
	for reply, want := range map[Reply]string{
		{Kind: "out", Text: "a\nb"}: `{"out":"a\nb"}`,
		{Kind: "exit", Code: 2}:     `{"exit":2}`,
	} {
		got, err := json.Marshal(reply)
		if err != nil || string(got) != want {
			t.Errorf("%+v: %s %v", reply, got, err)
		}
	}
}

func TestARequestRoundTripsThroughAHandler(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- Serve(server, func(r Request) Answer {
			return Answer{Out: r.Command + " " + strings.Join(r.Args, ","), Err: "careful", Exit: 3}
		})
		server.Close()
	}()
	var seen []Reply
	code, err := Exchange(client, Request{Command: "theme", Args: []string{"set", "paper"}, Cwd: "/tmp"}, func(r Reply) { seen = append(seen, r) })
	if err != nil || code != 3 {
		t.Fatalf("exchange: %d %v", code, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	want := []Reply{{Kind: "out", Text: "theme set,paper"}, {Kind: "err", Text: "careful"}}
	if len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Errorf("seen: %+v", seen)
	}
}

func TestAnAppThatHangsUpIsAnErrorNotASuccess(t *testing.T) {
	client, server := net.Pipe()
	go func() {
		buf := make([]byte, 1024)
		_, _ = server.Read(buf)
		server.Close()
	}()
	if _, err := Exchange(client, Request{Command: "theme"}, func(Reply) {}); err == nil {
		t.Error("a hang-up succeeded")
	}
}

func TestAStrayClientIsAnsweredThatItIsNoRequest(t *testing.T) {
	client, server := net.Pipe()
	go func() {
		_ = Serve(server, func(Request) Answer { return OK("no") })
		server.Close()
	}()
	go func() { _, _ = client.Write([]byte("hello\n")) }()
	buf := make([]byte, 4096)
	n, _ := client.Read(buf)
	if !strings.Contains(string(buf[:n]), "Not a request") {
		t.Errorf("answered: %s", buf[:n])
	}
}

const toolSchema = `{"type":"object","properties":{"text":{"type":"string"},"limit":{"type":"integer"},"exact":{"type":"boolean"}},"required":["text"]}`

func TestOptionsAreReadAsTheSchemaTypesThem(t *testing.T) {
	got, err := Arguments([]byte(toolSchema), []string{"--text", "hello world", "--limit=3", "--exact"})
	if err != nil || string(got) != `{"exact":true,"limit":3,"text":"hello world"}` {
		t.Errorf("%s %v", got, err)
	}
}

func TestJSONGivesEveryArgumentAtOnce(t *testing.T) {
	got, err := Arguments([]byte(toolSchema), []string{"--json", `{"text":"a","limit":2}`})
	if err != nil || string(got) != `{"limit":2,"text":"a"}` {
		t.Errorf("%s %v", got, err)
	}
}

func TestAWrongOptionOrValueIsRefusedWithWhy(t *testing.T) {
	if _, err := Arguments([]byte(toolSchema), []string{"--text", "a", "--nope", "1"}); err == nil || !strings.Contains(err.Error(), "--nope") {
		t.Errorf("unknown option: %v", err)
	}
	if _, err := Arguments([]byte(toolSchema), []string{"--text", "a", "--limit", "many"}); err == nil || !strings.Contains(err.Error(), "integer") {
		t.Errorf("not an integer: %v", err)
	}
	if _, err := Arguments([]byte(toolSchema), []string{"--limit", "1"}); err == nil || err.Error() != "Missing: --text." {
		t.Errorf("missing: %v", err)
	}
}

func TestAToolsUsageListsItsOptions(t *testing.T) {
	text := ToolUsage("catvinci.word-count", "word-count", "count", "Counts words.", []byte(toolSchema))
	if !strings.HasPrefix(text, "Usage: textus word-count count [--exact <boolean>] [--limit <integer>] --text <string>") {
		t.Error(text)
	}
}

func TestHelpListsEveryCommandButTheOnesKeptForLater(t *testing.T) {
	out := Overview("1.0", "  (none)")
	for _, c := range Commands {
		line := "\n  " + c.Name + strings.Repeat(" ", 8-len(c.Name)) + "  "
		if strings.Contains(out, line) != (c.Runs != NotYet) {
			t.Errorf("%s in help: %v", c.Name, !strings.Contains(out, line))
		}
	}
}
