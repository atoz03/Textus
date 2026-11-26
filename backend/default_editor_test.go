package backend

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestWindowsListsTextusUnderOpenWithWithoutTakingTheDefault(t *testing.T) {
	exe := `C:\Users\me\AppData\Local\Textus\Textus.exe`
	values := windowsRegistryValues(exe)
	find := func(key, name string) *registryValue {
		for i, v := range values {
			if v.key == key && v.name == name {
				return &values[i]
			}
		}
		return nil
	}
	text := func(key, name, want string) {
		t.Helper()
		if v := find(key, name); v == nil || v.empty || v.text != want {
			t.Errorf(`%s\%s: %+v, want %q`, key, name, v, want)
		}
	}
	for _, ext := range []string{".md", ".markdown"} {
		if v := find(`Software\Classes\`+ext+`\OpenWithProgids`, "Textus.Markdown"); v == nil || !v.empty {
			t.Errorf("%s: not under Open with: %+v", ext, v)
		}
		text(`Software\Classes\Applications\Textus.exe\SupportedTypes`, ext, "")
		text(`Software\Textus\Capabilities\FileAssociations`, ext, "Textus.Markdown")
		// The extension's default value is not ours to set.
		if v := find(`Software\Classes\`+ext, ""); v != nil {
			t.Errorf("%s's default set: %+v", ext, v)
		}
	}
	// The path is quoted: an install folder may have a space in it.
	open := `"` + exe + `" "%1"`
	// Its documents show their own icon, installed beside it.
	text(`Software\Classes\Textus.Markdown\DefaultIcon`, "", `"C:\Users\me\AppData\Local\Textus\doc.ico"`)
	text(`Software\Classes\Textus.Markdown\shell\open\command`, "", open)
	text(`Software\Classes\Applications\Textus.exe\shell\open\command`, "", open)
	text(`Software\RegisteredApplications`, "Textus", `Software\Textus\Capabilities`)
	// Everything stays under HKEY_CURRENT_USER's Software.
	for _, v := range values {
		if !strings.HasPrefix(v.key, `Software\`) {
			t.Errorf("outside Software: %s", v.key)
		}
	}
}

func TestWindowsOpensDefaultAppsAtTheRegisteredName(t *testing.T) {
	if defaultAppsSettings != "ms-settings:defaultapps?registeredAppUser=Textus" {
		t.Error(defaultAppsSettings)
	}
}

func TestMacOSClaimsEachResolvedTypeOnceAndMarkdownLast(t *testing.T) {
	got := contentTypes([]string{"net.daringfireball.markdown", "dyn.ah62d4rv4ge81e5pe"})
	if !slices.Equal(got, []string{"net.daringfireball.markdown", "dyn.ah62d4rv4ge81e5pe"}) {
		t.Errorf("resolved: %v", got)
	}
	if got := contentTypes(nil); !slices.Equal(got, []string{"net.daringfireball.markdown"}) {
		t.Errorf("none resolved: %v", got)
	}
}

func TestLinuxFindsThePackagedDesktopEntryInSearchOrder(t *testing.T) {
	dirs := []string{"/home/me/.local/share/applications", "/usr/share/applications"}
	installed := func(paths ...string) func(string) bool {
		return func(p string) bool { return slices.Contains(paths, p) }
	}
	if id, ok := installedDesktopID(dirs, installed(filepath.Join(dirs[1], "Textus.desktop"))); !ok || id != "Textus.desktop" {
		t.Errorf("deb: %s %v", id, ok)
	}
	if id, ok := installedDesktopID(dirs, installed(filepath.Join(dirs[1], "textus.desktop"))); !ok || id != "textus.desktop" {
		t.Errorf("AUR: %s %v", id, ok)
	}
	// An AppImage installs nothing.
	if id, ok := installedDesktopID(dirs, installed()); ok {
		t.Errorf("found %s", id)
	}
}

func TestLinuxSetsBothMarkdownMIMETypes(t *testing.T) {
	if got := xdgMimeDefaultArgs("Textus.desktop"); !slices.Equal(got, []string{"default", "Textus.desktop", "text/markdown", "text/x-markdown"}) {
		t.Errorf("args: %v", got)
	}
}
