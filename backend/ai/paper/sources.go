package paper

import (
	"fmt"
	"strconv"
	"strings"
)

// A paper's local sources - the files attached to it (`attached/...`, in
// the paper's folder) and the project's files, such as its reference
// library - and how they are cited and addressed among its sources.

// percentEncoded is s with every byte unsafe calls unsafe - and every
// control and non-ASCII byte - written as %XX.
func percentEncoded(s string, unsafe string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < 0x20 || b >= 0x7f || strings.IndexByte(unsafe, b) >= 0 {
			fmt.Fprintf(&out, "%%%02X", b)
		} else {
			out.WriteByte(b)
		}
	}
	return out.String()
}

const (
	// linkUnsafe is what a path can't hold inside a Markdown link: spaces,
	// brackets, parentheses and `%`.
	linkUnsafe = " ()[]<>%"
	// webLinkUnsafe is what a web address can't hold inside a Markdown
	// link, `%` aside so an escape already in it stays as it is: a DOI
	// like 10.1061/40927(243)238 cut the citation short at its `)`.
	webLinkUnsafe = " ()<>"
)

// linkSafe is a source's address as the paper's links carry it.
func linkSafe(url string) string {
	if isLocal(url) {
		return url
	}
	return percentEncoded(url, webLinkUnsafe)
}

// isLocal reports whether a source's address is a file's rather than a
// web page's.
func isLocal(address string) bool { return !strings.Contains(address, "://") }

// encoded is path with each part percent-encoded, so an address has no
// spaces.
func encoded(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = percentEncoded(p, linkUnsafe)
	}
	return strings.Join(parts, "/")
}

// decoded is address with its %XX escapes read back.
func decoded(address string) string {
	var out []byte
	for i := 0; i < len(address); i++ {
		if address[i] == '%' && i+2 < len(address) {
			if b, err := strconv.ParseUint(address[i+1:i+3], 16, 8); err == nil {
				out = append(out, byte(b))
				i += 2
				continue
			}
		}
		out = append(out, address[i])
	}
	return strings.ToValidUTF8(string(out), "\uFFFD")
}

// referenceAddress is a project file's address among the paper's sources:
// its path from the project folder (`references/...` for the reference
// library).
func referenceAddress(path string) string { return encoded(path) }

// referencePath is the path of the project file at a source's address;
// false for a web page or an attached file.
func referencePath(address string) (string, bool) {
	if _, attached := materialName(address); !isLocal(address) || attached {
		return "", false
	}
	return decoded(address), true
}

// materialAddress is an attached file's address among the paper's
// sources, by its path in attached/.
func materialAddress(name string) string { return Attached + "/" + encoded(name) }

// materialName is the attached file's path in attached/ at a source's
// address; false for another source.
func materialName(address string) (string, bool) {
	rest, ok := strings.CutPrefix(address, Attached)
	if !ok {
		return "", false
	}
	rest, ok = strings.CutPrefix(rest, "/")
	if !ok {
		return "", false
	}
	return decoded(rest), true
}

// citedFile is a local file a note cites.
type citedFile struct {
	// address is its address among the paper's sources.
	address, title string
	// path is where it is on disk, to check the note against.
	path string
}

// parseNumber reads s as a whole number: digits, a plus sign before them
// allowed.
func parseNumber(s string) (uint32, bool) {
	n, err := strconv.ParseUint(strings.TrimPrefix(s, "+"), 10, 32)
	return uint32(n), err == nil
}

// citedFileOf is the local file a note cites: `attached/...` from the
// paper's folder, or a file of the project folder - a bare name of an
// attached file too. False when cited names no readable file there (a
// source's number or a web address).
func (k *Kit) citedFileOf(cited string) (citedFile, bool) {
	cited = strings.TrimLeft(strings.TrimSpace(cited), "@")
	for strings.HasPrefix(cited, "./") {
		cited = cited[2:]
	}
	if _, number := parseNumber(strings.Trim(cited, "[]")); cited == "" || !isLocal(cited) || number || k.Files == nil {
		return citedFile{}, false
	}
	path := decoded(cited)
	found := func(folder, path string) (string, string, bool) {
		if folder == "" {
			return "", "", false
		}
		return k.Files.Resolve(folder, path)
	}
	title := func(path string) string { return path[strings.LastIndex(path, "/")+1:] }
	if name, ok := materialName(path); ok {
		if abs, _, ok := found(k.Folder, path); ok {
			return citedFile{materialAddress(name), title(name), abs}, true
		}
		return citedFile{}, false
	}
	if abs, rel, ok := found(k.Root, path); ok {
		return citedFile{referenceAddress(rel), title(rel), abs}, true
	}
	if abs, _, ok := found(k.Folder, Attached+"/"+path); ok {
		return citedFile{materialAddress(path), title(path), abs}, true
	}
	return citedFile{}, false
}
