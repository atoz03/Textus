package backend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"textus/backend/ai"
)

// What is remembered written: a memory or a library entry kept, updated
// in place or moved between scopes, undone, deleted or taken back, and the
// pass's and the tidy's changes made.

// memoryStem is a short, readable file stem from a memory's first words.
func memoryStem(text string) string {
	stem := safeFileStem(text, "memory", 48)
	var out strings.Builder
	for _, c := range stem {
		if c == '-' && strings.HasSuffix(out.String(), "-") {
			continue
		}
		out.WriteRune(c)
	}
	return strings.Trim(out.String(), "-")
}

// remember keeps one memory: validated, a duplicate refused, written (or a
// library entry) whole. The error is what the model is told instead.
func (d memoryDirs) remember(args ai.RememberArgs, source ai.MemorySource, now string) (ai.Remembering, error) {
	text := strings.TrimSpace(args.Text)
	if text == "" {
		return ai.Remembering{}, errors.New("`text` is required: the memory itself, in one or a few sentences.")
	}
	if n := utf8.RuneCountInString(text); n > ai.MaxMemoryTextChars {
		return ai.Remembering{}, fmt.Errorf("That is too long for a memory (%d characters): keep it to one or a few sentences, at most %d characters.", n, ai.MaxMemoryTextChars)
	}
	if strings.TrimSpace(args.Kind) == "fact" {
		return d.rememberFact(args, text, source, now)
	}
	kind, ok := ai.ParseMemoryKind(args.Kind)
	if !ok {
		return ai.Remembering{}, errors.New("`kind` must be requirement, preference, style or fact.")
	}
	var replaced *located
	if id := trimmed(args.Replaces); id != "" {
		loc, err := d.locate(id)
		if err != nil {
			return ai.Remembering{}, err
		}
		if info, err := os.Stat(loc.path); loc.folder != memoryFolder || err != nil || !info.Mode().IsRegular() {
			return ai.Remembering{}, fmt.Errorf("No memory `%s` to replace - use an id listed under what you remember, or leave `replaces` out.", id)
		}
		replaced = &loc
	}
	key := ai.Normalized(text)
	for _, m := range d.load().Items {
		if ai.Normalized(m.Text) == key {
			return ai.Remembering{}, fmt.Errorf("Already remembered as `%s` - nothing saved.", m.ID)
		}
	}
	var (
		scope    ai.Scope
		path     string
		previous *string
		record   ai.Remembered
	)
	if replaced != nil {
		raw, err := os.ReadFile(replaced.path)
		if err != nil {
			return ai.Remembering{}, err
		}
		content := string(raw)
		fields, _ := ai.FieldsAndBody(content)
		scope, path, previous, record = replaced.scope, replaced.path, &content, ai.RememberedFromFields(fields)
	} else {
		// The scope rule (also in the prompt): without a project,
		// everything is personal.
		scope = ai.ScopePersonal
		if s, ok := ai.ParseScope(deref(args.Scope)); (!ok || s == ai.ScopeProject) && d.project != "" {
			scope = ai.ScopeProject
		}
		dir, ok := d.folder(scope, memoryFolder)
		if !ok {
			return ai.Remembering{}, errors.New("Nowhere to keep memories: the app's config folder isn't available.")
		}
		path = freeMemoryPath(dir, memoryStem(text))
	}
	stamp(&record, source, now)
	content := fmt.Sprintf("---\nkind: %s\n%s---\n%s\n", kind, record.Lines(), text)
	if err := writeMemoryFile(path, content); err != nil {
		return ai.Remembering{}, err
	}
	return ai.Remembering{Remembered: memoryID(scope, memoryFolder, path), Scope: scope, Created: previous == nil, Text: text, Previous: previous}, nil
}

// stamp dates a record now, from source.
func stamp(record *ai.Remembered, source ai.MemorySource, now string) {
	if record.Created == "" {
		record.Created = now
	}
	record.Updated = now
	record.Conversation, record.Title, record.Document = source.Conversation, source.Title, source.Document
}

// trimmed is *s trimmed, "" for none.
func trimmed(s *string) string { return strings.TrimSpace(deref(s)) }

// rememberFact keeps a fact about the work: a library entry (`name`,
// `aliases`), new or one Textus AI wrote before - never one the user
// wrote.
func (d memoryDirs) rememberFact(args ai.RememberArgs, text string, source ai.MemorySource, now string) (ai.Remembering, error) {
	dir, ok := d.folder(ai.ScopeProject, libraryFolder)
	if d.project == "" || !ok {
		return ai.Remembering{}, errors.New("There is no project folder (the document isn't saved, or its folder is too broad), so there is no library to keep facts about the work in. Don't remember it; if it matters, tell the user that saving the document lets you.")
	}
	name := ai.OneLine(deref(args.Name))
	if name == "" {
		return ai.Remembering{}, errors.New("`name` is required for a fact: the person, place or term it is about.")
	}
	var aliases []string
	for _, a := range args.Aliases {
		a = strings.TrimSpace(strings.NewReplacer(",", " ", "，", " ", "、", " ", "[", " ", "]", " ").Replace(ai.OneLine(a)))
		if strings.ToLower(a) != strings.ToLower(name) && !ai.TooShortAnAlias(a) {
			aliases = append(aliases, a)
		}
	}
	library := readLibrary(d.project)
	stemOf := func(e *ai.LibraryEntry) string { return fileStem(e.File) }
	replaces := trimmed(args.Replaces)
	var existing *ai.LibraryEntry
	if replaces != "" {
		loc, err := d.locate(replaces)
		if err != nil {
			return ai.Remembering{}, err
		}
		for i := range library {
			if loc.folder == libraryFolder && stemOf(&library[i]) == fileStem(loc.path) {
				existing = &library[i]
				break
			}
		}
		if existing == nil {
			return ai.Remembering{}, fmt.Errorf("No library entry `%s` to replace.", replaces)
		}
	} else {
		// The entry the fact is about, by its name or an alias: a fact
		// about 林晚 belongs in her entry, not one beside it that says the
		// opposite.
		called := func(n string) bool {
			n = strings.ToLower(n)
			if n == strings.ToLower(name) {
				return true
			}
			for _, a := range aliases {
				if strings.ToLower(a) == n {
					return true
				}
			}
			return false
		}
	find:
		for i := range library {
			for _, n := range append([]string{library[i].Name}, library[i].Aliases...) {
				if called(n) {
					existing = &library[i]
					break find
				}
			}
		}
	}
	if existing != nil {
		id := "project/" + libraryFolder + "/" + stemOf(existing)
		if existing.Remembered == nil {
			return ai.Remembering{}, fmt.Errorf("The library's entry for %s (`%s`) is the user's own, and you never change it: tell the user what to add to it instead.", existing.Name, id)
		}
		if strings.Contains(ai.Normalized(existing.Body), ai.Normalized(text)) {
			return ai.Remembering{}, fmt.Errorf("Already remembered as `%s` - nothing saved.", id)
		}
		// Found without `replaces`, the entry keeps what it said and the
		// fact is added (what held before a change still holds for earlier
		// chapters); with `replaces`, the fact is the entry rewritten.
		if replaces == "" {
			all := append([]string{}, existing.Aliases...)
			for _, a := range append([]string{name}, aliases...) {
				if strings.ToLower(a) == strings.ToLower(existing.Name) {
					continue
				}
				known := false
				for _, b := range all {
					known = known || strings.ToLower(b) == strings.ToLower(a)
				}
				if !known {
					all = append(all, a)
				}
			}
			name, aliases = existing.Name, all
			text = strings.TrimRight(existing.Body, " \t\r\n") + "\n" + text
		}
	}
	var (
		path     string
		previous *string
		record   ai.Remembered
	)
	if existing != nil {
		path = filepath.Join(dir, stemOf(existing)+".md")
		raw, err := os.ReadFile(path)
		if err != nil {
			return ai.Remembering{}, err
		}
		content := string(raw)
		previous = &content
		if existing.Remembered != nil {
			record = *existing.Remembered
		}
	} else {
		path = freeMemoryPath(dir, safeFileStem(name, "entry", 80))
	}
	stamp(&record, source, now)
	content := fmt.Sprintf("---\nname: %s\naliases: [%s]\n%s---\n%s\n", name, strings.Join(aliases, ", "), record.Lines(), text)
	if err := writeMemoryFile(path, content); err != nil {
		return ai.Remembering{}, err
	}
	return ai.Remembering{
		Remembered: memoryID(ai.ScopeProject, libraryFolder, path), Scope: ai.ScopeProject, Created: previous == nil,
		Text: text, Previous: previous, Name: &name,
	}, nil
}

// keep is one memory kept as it was asked for: a suggestion's Remember.
func (d memoryDirs) keep(kind, scope, text string, source ai.MemorySource) (ai.Remembering, error) {
	return d.remember(ai.RememberArgs{Kind: kind, Scope: &scope, Text: text}, source, memoryTimestamp(time.Now()))
}

// undo undoes a remember call: what it created deleted, or the file put
// back as it was before it updated it. Only ever touches what Textus AI
// wrote.
func (d memoryDirs) undo(id string, previous *string) error {
	loc, err := d.locate(id)
	if err != nil {
		return err
	}
	ours := func(content string) bool { return loc.folder == memoryFolder || ai.RememberedFromFile(content) != nil }
	if previous == nil {
		raw, err := os.ReadFile(loc.path)
		if err != nil {
			return errors.New("GONE")
		}
		if !ours(string(raw)) {
			return codeError("notRemembered")
		}
		return os.Remove(loc.path)
	}
	if !ours(*previous) {
		return codeError("notRemembered")
	}
	// A forget undone: the file is back, its forgotten copy goes.
	d.dropForgottenCopy(loc.path, *previous)
	return writeMemoryFile(loc.path, *previous)
}

// moveTo moves a memory to the other scope - its file to the other
// folder - and answers its new id.
func (d memoryDirs) moveTo(id string, scope ai.Scope) (string, error) {
	loc, err := d.locate(id)
	if err != nil {
		return "", err
	}
	if loc.folder != memoryFolder {
		return "", errors.New("Only memories move between personal and project.")
	}
	if loc.scope == scope {
		return id, nil
	}
	dir, ok := d.folder(scope, memoryFolder)
	if !ok {
		return "", codeError("noProject")
	}
	raw, err := os.ReadFile(loc.path)
	if err != nil {
		return "", errors.New("GONE")
	}
	target := freeMemoryPath(dir, fileStem(loc.path))
	if err := writeMemoryFile(target, string(raw)); err != nil {
		return "", err
	}
	if err := os.Remove(loc.path); err != nil {
		return "", err
	}
	return memoryID(scope, memoryFolder, target), nil
}

// delete sets a memory or a library entry aside in its folder's
// .forgotten/, to restore.
func (d memoryDirs) delete(id string) error {
	loc, err := d.locate(id)
	if err != nil {
		return err
	}
	if !exists(loc.path) {
		return nil
	}
	return d.setAside(loc.path)
}

// forget takes back a memory, or a library entry Textus AI wrote: deleted,
// with what it was for Undo to put back.
func (d memoryDirs) forget(id string) (ai.Remembering, error) {
	loc, err := d.locate(id)
	if err != nil {
		return ai.Remembering{}, err
	}
	raw, err := os.ReadFile(loc.path)
	if err != nil {
		return ai.Remembering{}, fmt.Errorf("No memory `%s`.", id)
	}
	content := string(raw)
	if loc.folder == libraryFolder && ai.RememberedFromFile(content) == nil {
		return ai.Remembering{}, errors.New("The user's own library entry.")
	}
	fields, body := ai.FieldsAndBody(content)
	var name *string
	if n, ok := fields["name"]; ok && loc.folder == libraryFolder {
		name = &n
	}
	if err := d.delete(id); err != nil {
		return ai.Remembering{}, err
	}
	return ai.Remembering{Remembered: id, Scope: loc.scope, Text: strings.TrimSpace(body), Previous: &content, Name: name, Forgot: true}, nil
}

// applyChanges makes the pass's changes, and answers what each did, for
// the chips. A change the writer refuses (a duplicate, the user's own
// entry, a fact with no project) is left out.
func (d memoryDirs) applyChanges(changes []ai.MemoryChange, source ai.MemorySource, now string) []ai.Remembering {
	made := []ai.Remembering{}
	for _, c := range changes {
		id := c.TheID()
		switch op := c.TheOp(); op {
		case "forget":
			if id == "" {
				continue
			}
			if r, err := d.forget(id); err == nil {
				made = append(made, r)
			}
		case "add", "update":
			args := ai.RememberArgs{Kind: c.Kind, Scope: c.Scope, Text: c.Text, Name: c.Name, Aliases: c.Aliases}
			// An update names what it updates; one whose id names nothing
			// (a fact's entry by its name, made up) is kept as an addition -
			// a fact then joins the entry it is about.
			update := args
			if op == "update" && id != "" {
				update.Replaces = &id
			}
			r, err := d.remember(update, source, now)
			if err != nil {
				r, err = d.remember(args, source, now)
			}
			if err == nil {
				made = append(made, r)
			}
		}
	}
	return made
}

// passInput is what the pass is shown for exchange.
func (d memoryDirs) passInput(exchange ai.Exchange) string {
	var library []ai.LibraryEntry
	if d.project != "" {
		library = readLibrary(d.project)
	}
	return ai.PassInput(d.load(), library, d.project != "", exchange)
}

// modelCall asks the conversation's model: instructions, then the input.
type modelCall func(instructions, input string) (string, error)

// runPass is one pass: shown exchange, asked through call, its changes
// made. An answer with no JSON in it is asked for again, once, as an empty
// answer lost a "forget that".
func (d memoryDirs) runPass(exchange ai.Exchange, source ai.MemorySource, call modelCall) ([]ai.Remembering, error) {
	input := d.passInput(exchange)
	answer, err := call(ai.PassInstructions, input)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(answer, "{") {
		if answer, err = call(ai.PassInstructions, input); err != nil {
			return nil, err
		}
	}
	return d.applyChanges(ai.ParseChanges(answer), source, memoryTimestamp(time.Now())), nil
}
