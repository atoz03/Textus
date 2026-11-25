package backend

import (
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"textus/backend/ai"
	"textus/backend/ai/aicompat"
)

// The agent's tools that reach past the document: the project folder's and
// the attached files (find, search, read, and in General mode write and
// edit), the reference library, and the user's earlier conversations.

// maxListedFiles is how much of the project find_files with no name lists:
// enough for reference material, small enough that a huge folder can't
// blow up the prompt.
const maxListedFiles = 200

const noFiles = "There is no project folder for this document (it hasn't been saved, or its folder is too broad to use) and nothing is attached, so there are no files to read."

// agentFolders are the folders one request's file tools read: the project
// folder and the conversation's scratch folder ("" for none of either).
type agentFolders struct {
	root, scratch string
	// seesImages: read_file shows an image to the model rather than
	// refusing it.
	seesImages bool
	// data is the app's data folder, where the editor keeps the revisions
	// read_file says are waiting.
	data string
}

// pathArgument is the string key of a call's arguments, "" for none.
func pathArgument(arguments, key string) string {
	if args, ok := ai.ParseArgs(arguments); ok {
		return args.StrOr(key)
	}
	return ""
}

// folderOf is the folder path is in: the scratch folder for
// `attached/...`, else the project folder.
func (f agentFolders) folderOf(path string) string {
	rest, ok := strings.CutPrefix(strings.TrimLeft(path, "@/"), attachedFolder)
	if ok && strings.HasPrefix(rest, "/") {
		return f.scratch
	}
	return f.root
}

// inEachFolder is run over the project folder and the attached files,
// each answer under its own heading when there are both.
func (f agentFolders) inEachFolder(run func(folder string) string) string {
	switch {
	case f.root != "" && f.scratch != "":
		return fmt.Sprintf("In the project folder:\n%s\n\nIn the attached files:\n%s", run(f.root), run(f.scratch))
	case f.root != "":
		return run(f.root)
	case f.scratch != "":
		return run(f.scratch)
	}
	return noFiles
}

// listFiles is the folder's files the tools can read, images marked:
// find_files with no name.
func (f agentFolders) listFiles(root string) string {
	walked := walkReadableFiles(root, f.seesImages)
	if len(walked.files) == 0 {
		return "The project folder has no files to read."
	}
	var lines []string
	for _, rel := range walked.files[:min(len(walked.files), maxListedFiles)] {
		lines = append(lines, listedFile(rel))
	}
	if len(walked.files) > maxListedFiles || walked.truncated {
		more := fmt.Sprint(max(len(walked.files)-maxListedFiles, 0))
		if walked.truncated {
			more += " or more"
		}
		lines = append(lines, fmt.Sprintf("... (%s more - use find_files for a file by name, search_files for words inside files)", more))
	}
	return strings.Join(lines, "\n")
}

// tools are find_files, search_files and read_file over the folders.
func (f agentFolders) tools() []ai.Tool {
	findFilesTool := func(_ *ai.ToolContext, arguments string) string {
		name := pathArgument(arguments, "name")
		return f.inEachFolder(func(root string) string {
			if strings.TrimSpace(name) == "" {
				return f.listFiles(root)
			}
			return findFiles(root, name, f.seesImages)
		})
	}
	searchFilesTool := func(_ *ai.ToolContext, arguments string) string {
		query := pathArgument(arguments, "query")
		return f.inEachFolder(func(root string) string { return searchFiles(root, query) })
	}
	return []ai.Tool{
		{Spec: ai.ToolSpecOf("find_files", f.seesImages), Access: ai.ReadOnly, Modes: ai.EveryMode, Text: findFilesTool},
		{Spec: ai.ToolSpecOf("search_files", false), Access: ai.ReadOnly, Modes: ai.EveryMode, Text: searchFilesTool},
		{Spec: ai.ToolSpecOf("read_file", f.seesImages), Access: ai.ReadOnly, Modes: ai.EveryMode, Run: f.readFile},
	}
}

// readFile reads a text file or document from inside the folders, whole
// or a line range, or shows an image to a model that sees them.
func (f agentFolders) readFile(_ *ai.ToolContext, call aicompat.ToolCall) aicompat.ToolOutput {
	// find_files marks images "(image)"; a model may copy it.
	path := strings.TrimRight(strings.TrimSuffix(strings.TrimSpace(pathArgument(call.Arguments, "path")), "(image)"), " \t\r\n")
	root := f.folderOf(path)
	if root == "" {
		return aicompat.Text(noFiles)
	}
	if fileKindOf(path) == kindImage && f.seesImages {
		image, err := readImage(root, path)
		if err != nil {
			return aicompat.Text(err.Error())
		}
		return aicompat.ToolOutput{Text: fmt.Sprintf("Here is `%s` (%s).", image.Name, image.Mime), Images: []aicompat.ImageAttachment{image}}
	}
	args, ok := ai.ParseArgs(call.Arguments)
	if !ok {
		args = ai.NewArgs()
	}
	start, _ := args.Int("start_line")
	end, _ := args.Int("end_line")
	text, err := readPart(root, path, lineRange{start, end})
	if err != nil {
		// Refused (outside the folder, hidden) or unread: nothing of it.
		return aicompat.Text(err.Error())
	}
	// What it left waiting there: its revisions, a change that met edits.
	// Named from the folder as the editor names it, as file changes are.
	if note := waitingNote(f.data, filepath.Join(root, strings.TrimLeft(path, "@"))); note != "" {
		text += "\n\n" + note
	}
	return aicompat.Text(text)
}

// readImage is read_file of an image, within the size every provider
// accepts. Textus can't shrink an image, so a larger one is refused with
// why.
func readImage(root, rel string) (aicompat.ImageAttachment, error) {
	path, rel, err := resolveIn(root, rel)
	if err != nil {
		return aicompat.ImageAttachment{}, err
	}
	mime := imageMime(fileExt(path))
	if mime == "" {
		return aicompat.ImageAttachment{}, fmt.Errorf("`%s` isn't an image Textus can show (PNG, JPEG, GIF or WebP) - read_file reads text and documents.", rel)
	}
	if size := sizeOf(path); size > maxImageBytes {
		return aicompat.ImageAttachment{}, fmt.Errorf("`%s` is too large to show (%s; the limit is %s), and Textus can't make images smaller - ask the user for a smaller copy.", rel, megabytes(size), megabytes(maxImageBytes))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return aicompat.ImageAttachment{}, fmt.Errorf("`%s` can't be read: %v", rel, err)
	}
	return aicompat.ImageAttachment{Name: rel, Mime: mime, DataBase64: base64.StdEncoding.EncodeToString(raw)}, nil
}

// selectedPicture is the picture a message's selection is, when it is one
// picture and nothing else, in the document's folder, and PNG, JPEG, GIF
// or WebP.
func selectedPicture(message, docPath string) (aicompat.ImageAttachment, bool) {
	src, ok := ai.SelectedPicture(message)
	if !ok || docPath == "" {
		return aicompat.ImageAttachment{}, false
	}
	image, err := readImage(filepath.Dir(docPath), src)
	return image, err == nil
}

// referencesTool is search_references, when the project folder root has a
// references folder to search.
func referencesTool(root string) (ai.Tool, bool) {
	if root == "" {
		return ai.Tool{}, false
	}
	if info, err := os.Stat(filepath.Join(root, referencesDir)); err != nil || !info.IsDir() {
		return ai.Tool{}, false
	}
	spec := ai.ToolSpecOf("search_references", false)
	spec.Description = strings.Replace(spec.Description, "{}", referencesDir, 1)
	return ai.Tool{Spec: spec, Access: ai.ReadOnly, Modes: ai.EveryMode, Text: func(_ *ai.ToolContext, arguments string) string {
		return searchReferences(root, pathArgument(arguments, "query"))
	}}, true
}

// tools are write_file and edit_file, making this request's changes.
func (e *fileEdits) tools() []ai.Tool {
	tool := func(name string, change fileChangeFunc) ai.Tool {
		return ai.Tool{Spec: ai.ToolSpecOf(name, false), Access: ai.ProposesInItsCall, Modes: ai.EditingModes, Run: func(_ *ai.ToolContext, call aicompat.ToolCall) aicompat.ToolOutput {
			text, recorded := e.run(call.Arguments, change)
			return aicompat.ToolOutput{Text: text, CallArguments: recorded}
		}}
	}
	return []ai.Tool{tool("write_file", writeFileChange), tool("edit_file", editFileChange)}
}

// conversationsShown is how many places search_conversations shows.
const conversationsShown = 8

// conversationDate is when a conversation was last changed, as a UTC
// date.
func conversationDate(updatedAt jsontext.Value) string {
	var ms float64
	_ = json.Unmarshal(updatedAt, &ms)
	return time.UnixMilli(int64(ms)).UTC().Format("2006-01-02")
}

// describeConversations is what the model is shown of the hits.
func describeConversations(query string, hits []jsontext.Value) string {
	if len(hits) == 0 {
		return fmt.Sprintf("No earlier conversation has \"%s\" in it. Try other words, or ask the user.", query)
	}
	var lines []string
	for _, raw := range hits[:min(len(hits), conversationsShown)] {
		var hit struct {
			Title     string         `json:"title"`
			UpdatedAt jsontext.Value `json:"updatedAt"`
			Role      string         `json:"role"`
			Snippet   string         `json:"snippet"`
		}
		_ = json.Unmarshal(raw, &hit)
		who := "you"
		if hit.Role == "user" {
			who = "the user"
		}
		lines = append(lines, fmt.Sprintf("- %s \"%s\" - %s said: %s", conversationDate(hit.UpdatedAt), hit.Title, who, hit.Snippet))
	}
	return fmt.Sprintf("Earlier conversations with \"%s\", newest first:\n%s", query, strings.Join(lines, "\n"))
}

// searchConversationsTool searches history, the user's earlier
// conversations with Textus AI, when they refer to one.
func searchConversationsTool(history *ChatHistory) ai.Tool {
	return ai.Tool{Spec: ai.ToolSpecOf("search_conversations", false), Access: ai.ReadOnly, Modes: ai.EveryMode, Text: func(_ *ai.ToolContext, arguments string) string {
		query := strings.TrimSpace(pathArgument(arguments, "query"))
		if query == "" {
			return "`query` is empty - search for a word or two."
		}
		return describeConversations(query, history.search(query, conversationsShown))
	}}
}
