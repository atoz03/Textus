//go:build darwin && !ios

package backend

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/ebitengine/purego"
)

// LaunchServices, through CoreServices, reached without cgo.

const (
	coreFoundationPath = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	coreServicesPath   = "/System/Library/Frameworks/CoreServices.framework/CoreServices"
	// cfStringUTF8 is kCFStringEncodingUTF8; lsRolesAll kLSRolesAll, viewer
	// and editor alike.
	cfStringUTF8 = 0x08000100
	lsRolesAll   = 0xFFFFFFFF
	// filenameExtensionTag is kUTTagClassFilenameExtension's value.
	filenameExtensionTag = "public.filename-extension"
)

type launchServices struct {
	createString  func(alloc uintptr, s string, encoding uint32) uintptr
	stringLength  func(s uintptr) int64
	getString     func(s uintptr, buf *byte, size int64, encoding uint32) bool
	release       func(cf uintptr)
	preferredType func(class, tag, conformingTo uintptr) uintptr
	copyHandler   func(contentType uintptr, role uint32) uintptr
	setHandler    func(contentType uintptr, role uint32, handler uintptr) int32
}

// ls is LaunchServices, loaded the first time; nil where it can't be.
var ls = sync.OnceValue(func() *launchServices {
	cf, err := purego.Dlopen(coreFoundationPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return nil
	}
	cs, err := purego.Dlopen(coreServicesPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return nil
	}
	l := &launchServices{}
	purego.RegisterLibFunc(&l.createString, cf, "CFStringCreateWithCString")
	purego.RegisterLibFunc(&l.stringLength, cf, "CFStringGetLength")
	purego.RegisterLibFunc(&l.getString, cf, "CFStringGetCString")
	purego.RegisterLibFunc(&l.release, cf, "CFRelease")
	purego.RegisterLibFunc(&l.preferredType, cs, "UTTypeCreatePreferredIdentifierForTag")
	purego.RegisterLibFunc(&l.copyHandler, cs, "LSCopyDefaultRoleHandlerForContentType")
	purego.RegisterLibFunc(&l.setHandler, cs, "LSSetDefaultRoleHandlerForContentType")
	return l
})

// str is s as a CFString the caller releases.
func (l *launchServices) str(s string) uintptr { return l.createString(0, s, cfStringUTF8) }

// owned is a CFString returned under the Create rule as a string, released.
func (l *launchServices) owned(ref uintptr) (string, bool) {
	if ref == 0 {
		return "", false
	}
	defer l.release(ref)
	// UTF-8 takes at most four bytes for each UTF-16 unit, and a NUL.
	buf := make([]byte, l.stringLength(ref)*4+1)
	if !l.getString(ref, &buf[0], int64(len(buf)), cfStringUTF8) {
		return "", false
	}
	return string(buf[:strings.IndexByte(string(buf), 0)]), true
}

// markdownTypes are the types .md and .markdown resolve to, then Markdown's.
func (l *launchServices) markdownTypes() []string {
	class := l.str(filenameExtensionTag)
	defer l.release(class)
	var resolved []string
	for _, ext := range []string{"md", "markdown"} {
		tag := l.str(ext)
		if uti, ok := l.owned(l.preferredType(class, tag, 0)); ok {
			resolved = append(resolved, uti)
		}
		l.release(tag)
	}
	return contentTypes(resolved)
}

func editorStatus(context.Context) defaultEditorStatus {
	l := ls()
	if l == nil {
		return defaultEditorStatus{}
	}
	uti := l.str(l.markdownTypes()[0])
	defer l.release(uti)
	handler, ok := l.owned(l.copyHandler(uti, lsRolesAll))
	return defaultEditorStatus{Supported: true, IsDefault: ok && strings.EqualFold(handler, Identifier)}
}

func makeDefaultEditor(context.Context) error {
	l := ls()
	if l == nil {
		return fmt.Errorf("LaunchServices can't be reached")
	}
	bundle := l.str(Identifier)
	defer l.release(bundle)
	for _, uti := range l.markdownTypes() {
		contentType := l.str(uti)
		status := l.setHandler(contentType, lsRolesAll, bundle)
		l.release(contentType)
		if status != 0 {
			return fmt.Errorf("LaunchServices refused %s (OSStatus %d)", uti, status)
		}
	}
	return nil
}
