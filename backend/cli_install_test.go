package backend

import (
	"testing"
)

func TestTheWindowsPathIsAddedOnce(t *testing.T) {
	dir := `C:\Users\a\AppData\Local\Textus\bin`
	if !pathHas(`C:\x;c:\users\a\appdata\local\textus\BIN\;`, dir) {
		t.Error("not found as Windows compares")
	}
	if pathHas(`C:\x;C:\Users\a\AppData\Local\Textus`, dir) {
		t.Error("found its parent")
	}
	if got := pathWith("", dir); got != dir {
		t.Errorf("empty: %s", got)
	}
	if got := pathWith(`C:\x;`, dir); got != `C:\x;`+dir {
		t.Errorf("after: %s", got)
	}
}

func TestTheWindowsPathKeepsEveryEntryOrIsNotWritten(t *testing.T) {
	dir := `C:\Users\a\AppData\Local\Textus\bin`
	old := `C:\Python;%USERPROFILE%\go\bin;`
	next, ok := nextPath(&old, dir)
	if !ok || next != `C:\Python;%USERPROFILE%\go\bin;`+dir {
		t.Errorf("next: %s %v", next, ok)
	}
	// No PATH yet: only ours.
	if next, ok := nextPath(nil, dir); !ok || next != dir {
		t.Errorf("none: %s %v", next, ok)
	}
	// Nothing that could cut the user's PATH.
	for _, bad := range []string{"", " ", "a;b"} {
		if _, ok := nextPath(&old, bad); ok {
			t.Errorf("%q written", bad)
		}
	}
}

func TestOnlyAPathReadWholeIsText(t *testing.T) {
	utf16le := func(units ...uint16) []byte {
		var b []byte
		for _, u := range units {
			b = append(b, byte(u), byte(u>>8))
		}
		return b
	}
	text := func(s string) []uint16 {
		var units []uint16
		for _, r := range s {
			units = append(units, uint16(r))
		}
		return units
	}
	if got, ok := registryText(utf16le(append(text(`C:\é;D:\`), 0, 0)...)); !ok || got != `C:\é;D:\` {
		t.Errorf("text: %q %v", got, ok)
	}
	if got, ok := registryText(nil); !ok || got != "" {
		t.Errorf("empty: %q %v", got, ok)
	}
	for name, data := range map[string][]byte{
		"a NUL inside":   utf16le(append(text("C:"), 0, 'D')...),
		"a lone half":    utf16le('C', 0xD800, ';'),
		"an odd length":  {'C', 0, ';'},
		"a reversed one": utf16le(0xDC00, 0xD800),
	} {
		if got, ok := registryText(data); ok {
			t.Errorf("%s read as %q", name, got)
		}
	}
}

func TestScriptsRunTheBinaryMarkedAsFromATerminal(t *testing.T) {
	if got := windowsScript("Textus.exe"); got != "@echo off\r\nsetlocal\r\nset \"TEXTUS_CLI=1\"\r\n\"%~dp0..\\Textus.exe\" %*\r\n" {
		t.Errorf("windows: %q", got)
	}
	if got := linuxScript("/home/a/it's/Textus.AppImage"); got != "#!/bin/sh\nTEXTUS_CLI=1 exec '/home/a/it'\\''s/Textus.AppImage' \"$@\"\n" {
		t.Errorf("linux: %q", got)
	}
	want := "#!/bin/sh\nif [ -x '/Applications/Textus.app/Contents/MacOS/textus' ]; then\n  " +
		"TEXTUS_CLI=1 exec '/Applications/Textus.app/Contents/MacOS/textus' \"$@\"\nfi\n" +
		"exec open -a \"Textus\" \"$@\"\n"
	if got := macScript("/Applications/Textus.app/Contents/MacOS/textus", "Textus"); got != want {
		t.Errorf("mac: %q", got)
	}
}

func TestAScriptAnOlderTextusWroteNeedsUpdating(t *testing.T) {
	exe := "/Applications/Textus.app/Contents/MacOS/textus"
	current := func(s string) bool { return s == macScript(exe, "Textus") }
	older := "#!/bin/bash\nexec open -a \"Textus\" \"$@\"\n"
	now := macScript(exe, "Textus")
	if cliStatusOf(&older, current) != cliOutdated || cliStatusOf(&now, current) != cliInstalled || cliStatusOf(nil, current) != cliMissing {
		t.Error("status")
	}
}

func TestADevBuildKeepsTheScriptThatOpensTheInstalledApp(t *testing.T) {
	if got := macScript("", "Textus"); got != "#!/bin/bash\nexec open -a \"Textus\" \"$@\"\n" {
		t.Errorf("%q", got)
	}
}
