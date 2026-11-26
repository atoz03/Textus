package backend

import (
	"bufio"
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
)

// The system around the app, and the window commands the page's window
// object (src/platform/native/window.ts) is made of.

type SystemInfo struct {
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Webview string `json:"webview"`
}

func osVersion() string {
	switch runtime.GOOS {
	case "darwin":
		if out, err := exec.Command("sw_vers", "-productVersion").Output(); err == nil {
			return "macOS " + strings.TrimSpace(string(out))
		}
		return "macOS"
	case "windows":
		return "Windows"
	}
	name := "Linux"
	if raw, err := os.ReadFile("/etc/os-release"); err == nil {
		for line := range strings.SplitSeq(string(raw), "\n") {
			if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
				name = strings.Trim(v, `"`)
				break
			}
		}
	}
	if desktop := os.Getenv("XDG_CURRENT_DESKTOP"); desktop != "" {
		return name + " " + desktop
	}
	return name
}

func goArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	}
	return runtime.GOARCH
}

type LocalName struct {
	Language string `json:"language"`
	Name     string `json:"name"`
}

type FontFile struct {
	Path   string `json:"path"`
	Weight int    `json:"weight"`
	Italic bool   `json:"italic"`
}

type FontFamily struct {
	Name  string      `json:"name"`
	Local []LocalName `json:"local"`
	Files []FontFile  `json:"files"`
}

// fontFamilies lists the installed families through fontconfig, where
// there is one (Linux); elsewhere none, and Settings offers its own list.
func fontFamilies() []FontFamily {
	out, err := exec.Command("fc-list", "--format", "%{family}\n").Output()
	if err != nil {
		return []FontFamily{}
	}
	seen := map[string]bool{}
	list := []FontFamily{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		// "Noto Sans CJK SC,Noto Sans CJK SC Regular": the first name is
		// the one CSS knows.
		name := strings.TrimSpace(strings.SplitN(sc.Text(), ",", 2)[0])
		name = strings.ReplaceAll(name, `\-`, "-")
		key := strings.ToLower(name)
		if name == "" || strings.HasPrefix(name, ".") || seen[key] {
			continue
		}
		seen[key] = true
		list = append(list, FontFamily{Name: name, Local: []LocalName{}, Files: []FontFile{}})
	}
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
	return list
}

type MemoryState struct {
	TotalMb  int `json:"totalMb"`
	Pressure int `json:"pressure"`
}

func memoryState() MemoryState {
	state := MemoryState{TotalMb: 8192}
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return state
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				if kb, err := strconv.Atoi(fields[0]); err == nil {
					state.TotalMb = kb / 1024
				}
			}
		}
	}
	return state
}

type WindowGeometry struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// ---- the calling window ----

func (c *Call) win() (*appWindow, error) {
	if c.window == nil {
		return nil, errors.New("not called from a window")
	}
	return c.window, nil
}

func windowCommand(name string, fn func(w *appWindow)) {
	command(name, func(c *Call, _ none) (nothing, error) {
		w, err := c.win()
		if err != nil {
			return nil, err
		}
		fn(w)
		return nil, nil
	})
}

func init() {
	command("system_info", func(c *Call, _ none) (SystemInfo, error) {
		return SystemInfo{OS: osVersion(), Arch: goArch(), Webview: ""}, nil
	})
	command("system_font_families", func(c *Call, _ none) ([]FontFamily, error) { return fontFamilies(), nil })
	command("memory_state", func(c *Call, _ none) (MemoryState, error) { return memoryState(), nil })
	command("clipboard_text", func(c *Call, _ none) (*string, error) {
		text := mygo.Clipboard.ReadText()
		if text == "" {
			return nil, nil
		}
		return &text, nil
	})
	command("set_window_title", func(c *Call, a struct {
		Title string `json:"title"`
	}) (nothing, error) {
		if c.window != nil {
			c.window.win.SetTitle(a.Title)
		}
		return nil, nil
	})
	// The traffic lights' place (macOS) is set as a window opens; the
	// input source and native editing are the page's own here.
	command("set_window_bar_height", func(c *Call, _ none) (nothing, error) { return nil, nil })
	command("set_roman_input", func(c *Call, _ none) (nothing, error) { return nil, nil })
	command("edit_natively", func(c *Call, _ none) (bool, error) { return false, nil })
	command("drag_modifiers", func(c *Call, _ none) (map[string]bool, error) { return map[string]bool{"shift": false}, nil })
	command("release_instance", func(c *Call, _ none) (nothing, error) { return nil, nil })
	// After an update is installed: every window closes as at Quit (an
	// unsaved document asks, or is kept as a draft), then the new version
	// starts.
	command("quit_and_relaunch", func(c *Call, _ none) (nothing, error) {
		relaunchAfterQuit.Store(true)
		c.app.quit()
		return nil, nil
	})
	command("web_asset", func(c *Call, a struct {
		URL  string `json:"url"`
		Kind string `json:"kind"`
	}) (string, error) {
		return c.app.webAsset(c.ctx, a.URL, a.Kind)
	})
	command("send_problem_report", func(c *Call, a struct {
		Report jsontext.Value `json:"report"`
	}) (SentReport, error) {
		return sendProblemReport(c.ctx, a.Report)
	})

	// A tab moved to a window of its own: the new window takes it.
	command("move_tab_to_new_window", func(c *Call, a struct {
		Tab jsontext.Value `json:"tab"`
	}) (nothing, error) {
		return nil, c.app.openTabWindow(a.Tab, nil)
	})
	command("take_detached_tab", func(c *Call, _ none) (jsontext.Value, error) {
		c.app.mu.Lock()
		defer c.app.mu.Unlock()
		tab, ok := c.app.detachedTabs[c.Label()]
		if !ok {
			return jsontext.Value("null"), nil
		}
		delete(c.app.detachedTabs, c.Label())
		return tab, nil
	})

	// The page's window.
	command("window_close_listen", func(c *Call, a struct {
		On bool `json:"on"`
	}) (nothing, error) {
		w, err := c.win()
		if err != nil {
			return nil, err
		}
		w.closeListened.Store(a.On)
		return nil, nil
	})
	windowCommand("window_close", func(w *appWindow) { w.win.Close() })
	windowCommand("window_destroy", func(w *appWindow) { w.destroy() })
	windowCommand("window_minimize", func(w *appWindow) { w.win.Minimize() })
	windowCommand("window_toggle_maximize", func(w *appWindow) { w.win.ToggleMaximize() })
	windowCommand("window_show", func(w *appWindow) { w.win.Show(); w.win.Focus() })
	command("window_is_maximized", func(c *Call, _ none) (bool, error) {
		w, err := c.win()
		if err != nil {
			return false, err
		}
		return w.win.IsMaximized(), nil
	})
	command("window_is_fullscreen", func(c *Call, _ none) (bool, error) {
		w, err := c.win()
		if err != nil {
			return false, err
		}
		return w.win.IsFullScreen(), nil
	})
	command("window_set_fullscreen", func(c *Call, a struct {
		On bool `json:"on"`
	}) (nothing, error) {
		w, err := c.win()
		if err != nil {
			return nil, err
		}
		w.win.SetFullScreen(a.On)
		return nil, nil
	})
	command("window_set_always_on_top", func(c *Call, a struct {
		On bool `json:"on"`
	}) (nothing, error) {
		w, err := c.win()
		if err != nil {
			return nil, err
		}
		w.win.SetAlwaysOnTop(a.On)
		return nil, nil
	})
	command("window_bounds", func(c *Call, _ none) (WindowGeometry, error) {
		w, err := c.win()
		if err != nil {
			return WindowGeometry{}, err
		}
		b := w.win.Bounds()
		return WindowGeometry{X: b.X, Y: b.Y, Width: b.Width, Height: b.Height}, nil
	})
	command("document_dir", func(c *Call, _ none) (string, error) {
		return mygo.App.Path(mygo.PathDocuments)
	})
	command("temp_dir", func(c *Call, _ none) (string, error) { return os.TempDir(), nil })
	command("app_version", func(c *Call, _ none) (string, error) { return c.app.shared.Version, nil })
}
