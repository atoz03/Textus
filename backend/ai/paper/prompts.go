package paper

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// What the paper's stages tell the model, kept as text apart from the
// code: one Markdown file a stage in prompts/, compiled in. A file's
// `## name` headings each begin a prompt, `<file>.<name>`, which runs to
// the next heading, kept as written but for the blank lines around it;
// what comes before the first heading is a note for whoever edits the
// file. A prompt's `{{name}}` is filled in by the code from what it knows
// at the time - the paper, its notes, its outline; a lone brace is just a
// brace, as in the JSON a prompt asks for. A key with a dot in it,
// `{{shared.zh-filler}}`, is another prompt, taken in as the files are
// read: so a genre can take another's part (genres/*.md) and a rule the
// genres share is written once (shared.md).
//
// To try a prompt without building again, point TEXTUS_PAPER_PROMPTS at a
// folder laid out as prompts/ is: each file in it is read in place of the
// one compiled in, as the prompts are first used. Only a test build reads
// it (promptsOverride); the app as released says what is compiled in.

//go:embed prompts
var promptFS embed.FS

// promptFiles are every prompt file: its name, and its path in prompts/.
var promptFiles = [][2]string{
	{"choose", "choose.md"},
	{"finish", "finish.md"},
	{"research", "research.md"},
	{"outline", "outline.md"},
	{"draft", "draft.md"},
	{"review", "review.md"},
	{"shared", "shared.md"},
	{"genre.paper", "genres/paper.md"},
	{"genre.review", "genres/review.md"},
	{"genre.report", "genres/report.md"},
	{"genre.analysis", "genres/analysis.md"},
	{"genre.article", "genres/article.md"},
	{"genre.thread", "genres/thread.md"},
	{"genre.brief", "genres/brief.md"},
}

// overrideVariable names the folder prompt files are read from in place
// of the compiled ones.
const overrideVariable = "TEXTUS_PAPER_PROMPTS"

// promptsOverride: the folder overrideVariable names is read. Tests set
// it; the app as released says what is compiled in.
var promptsOverride = false

// promptText is the prompt files' texts by name: each from the folder the
// override names where it has one, else compiled in.
func promptTexts() [][2]string {
	folder := ""
	if promptsOverride {
		folder = os.Getenv(overrideVariable)
	}
	return promptTextsFrom(folder)
}

// promptTextsFrom is the prompt files' texts, each from folder where it
// has one.
func promptTextsFrom(folder string) [][2]string {
	out := make([][2]string, len(promptFiles))
	for i, f := range promptFiles {
		name, path := f[0], f[1]
		if folder != "" {
			if read, err := os.ReadFile(filepath.Join(folder, filepath.FromSlash(path))); err == nil {
				log.Printf("paper prompts: %s read from %s", path, overrideVariable)
				out[i] = [2]string{name, string(read)}
				continue
			}
		}
		text, err := promptFS.ReadFile("prompts/" + path)
		if err != nil {
			panic("paper prompts: " + err.Error())
		}
		out[i] = [2]string{name, string(text)}
	}
	return out
}

// parsePrompts are the prompts in text, the file file, by their full
// names.
func parsePrompts(file, text string) [][2]string {
	var out [][2]string
	var bodies []*strings.Builder
	for line := range strings.SplitSeq(text, "\n") {
		if name, ok := strings.CutPrefix(line, "## "); ok {
			out = append(out, [2]string{file + "." + strings.TrimSpace(name), ""})
			bodies = append(bodies, &strings.Builder{})
		} else if len(bodies) > 0 {
			body := bodies[len(bodies)-1]
			body.WriteString(line)
			body.WriteByte('\n')
		}
	}
	for i := range out {
		out[i][1] = strings.Trim(bodies[i].String(), "\n")
	}
	return out
}

// takeIn is text with each `{{other.prompt}}` in it replaced by that
// prompt from read, itself taken in the same way - at most a few deep, so
// a prompt can't take itself in forever.
func takeIn(text string, read map[string]string, depth int) string {
	var out strings.Builder
	rest := text
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			break
		}
		after := rest[start+2:]
		end := strings.Index(after, "}}")
		if end < 0 {
			break
		}
		key := after[:end]
		out.WriteString(rest[:start])
		if other, ok := read[key]; ok && strings.Contains(key, ".") && depth < 4 {
			out.WriteString(takeIn(other, read, depth+1))
		} else {
			if strings.Contains(key, ".") && strictPrompts {
				panic(fmt.Sprintf("no prompt %q to take in", key))
			}
			out.WriteString(rest[start : start+end+4])
		}
		rest = after[end+2:]
	}
	out.WriteString(rest)
	return out.String()
}

var allPrompts = sync.OnceValue(func() map[string]string {
	read := map[string]string{}
	for _, f := range promptTexts() {
		for _, p := range parsePrompts(f[0], f[1]) {
			read[p[0]] = p[1]
		}
	}
	all := make(map[string]string, len(read))
	for name, text := range read {
		all[name] = takeIn(text, read, 0)
	}
	return all
})

// prompt is the prompt name ("draft.instructions"). Every name the code
// asks for is in a file: the tests ask for each.
func prompt(name string) string {
	text, ok := allPrompts()[name]
	if !ok {
		panic(fmt.Sprintf("no prompt %q in backend/ai/paper/prompts", name))
	}
	return text
}

// fingerprint is a short mark of every prompt as they read, the same for
// the same prompts, so runs on different prompts are told apart.
func fingerprint() string {
	all := allPrompts()
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	slices.Sort(names)
	hash := sha256.New()
	for _, name := range names {
		hash.Write([]byte(name))
		hash.Write([]byte{0})
		hash.Write([]byte(all[name]))
		hash.Write([]byte{0})
	}
	return fmt.Sprintf("%x", hash.Sum(nil)[:6])
}

// kv is one value a prompt's `{{key}}` is filled with.
type kv struct {
	key   string
	value any
}

// fill is the prompt name with each `{{key}}` in it filled in from
// values.
func fill(name string, values ...kv) string {
	return render(prompt(name), values...)
}

// render is template with each `{{key}}` filled in. A key with no value,
// or a value no key takes, is a mistake in the prompt or in its caller:
// the tests fail on it, and the app leaves the key as it is.
func render(template string, values ...kv) string {
	var out strings.Builder
	used := make([]bool, len(values))
	rest := template
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			break
		}
		out.WriteString(rest[:start])
		after := rest[start+2:]
		end := strings.Index(after, "}}")
		if end < 0 {
			out.WriteString(rest[start:])
			rest = ""
			break
		}
		key := after[:end]
		i := slices.IndexFunc(values, func(v kv) bool { return v.key == key })
		if i >= 0 {
			used[i] = true
			fmt.Fprint(&out, values[i].value)
		} else {
			if strictPrompts {
				panic(fmt.Sprintf("the prompt's {{%s}} has no value", key))
			}
			out.WriteString(rest[start : start+end+4])
		}
		rest = after[end+2:]
	}
	out.WriteString(rest)
	if i := slices.Index(used, false); i >= 0 && strictPrompts {
		panic(fmt.Sprintf("no {{%s}} in the prompt takes its value", values[i].key))
	}
	return out.String()
}

// strictPrompts: a prompt's mistake panics rather than going out as
// written. The tests set it.
var strictPrompts = false
