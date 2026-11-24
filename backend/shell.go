package backend

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
)

// Links out of the app: web pages in the browser, files with the app the
// system has for them.

// ownHosts are the app's own origins on Windows, where its schemes are
// served as http://<scheme>.localhost.
var ownHosts = []string{"mygo.localhost", "textus-plugin.localhost", "asset.localhost"}

// mayLoad says whether a window may go to u: its own pages and the web's
// secure ones (iframes of a document); anything else opens outside.
func mayLoad(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "mygo", "textus-plugin", "asset", "about", "blob", "data", "https":
		return true
	case "http":
		host := u.Hostname()
		if containsString(ownHosts, host) {
			return true
		}
		if host == "localhost" || host == "127.0.0.1" {
			return mygo.IsDev()
		}
	}
	return false
}

// openOutside opens a web or mail link the window may not load in the
// system's browser or mail app; any other is dropped.
func openOutside(raw string) {
	u, err := url.Parse(raw)
	if err != nil {
		return
	}
	switch u.Scheme {
	case "http", "https", "mailto":
		_ = mygo.Shell.OpenExternal(u.String())
	}
}

const site = "https://textus.md"

func languageDir(language *string) (string, bool) {
	if language == nil {
		return "", false
	}
	dirs := map[string]string{"en": "", "zh": "zh/", "zh-Hant": "zh-hant/", "ja": "ja/", "ko": "ko/", "es": "es/", "fr": "fr/", "de": "de/", "ru": "ru/", "pt": "pt/"}
	dir, ok := dirs[*language]
	return dir, ok
}

func communityURL(language *string) string {
	if dir, ok := languageDir(language); ok {
		return site + "/" + dir + "community/"
	}
	return site + "/join"
}

func sponsorURL(language *string) string {
	dir, _ := languageDir(language)
	return site + "/" + dir + "sponsor/"
}

var tutorialClips = []string{"wysiwyg", "blocks", "modes-themes", "sidebar-export", "ai-panel", "continuation", "grammar", "revision", "review", "skills", "memory"}

func tutorialsURL(clip, language *string) (string, bool) {
	dir, _ := languageDir(language)
	page := site + "/" + dir + "tutorials/"
	if clip == nil {
		return page, true
	}
	if !containsString(tutorialClips, *clip) {
		return "", false
	}
	return page + "#" + *clip, true
}

func openCommunity(language *string) error { return mygo.Shell.OpenExternal(communityURL(language)) }
func openSponsor(language *string) error   { return mygo.Shell.OpenExternal(sponsorURL(language)) }

func openTutorials(clip, language *string) error {
	u, ok := tutorialsURL(clip, language)
	if !ok {
		return errorf("no such tutorial")
	}
	return mygo.Shell.OpenExternal(u)
}

var aboutPages = map[string]string{
	"github":  "https://github.com/CatVinci-Studio/Textus",
	"discord": "https://textus.md/discord",
	"qq":      "https://textus.md/qq",
	"themes":  "https://textus.md/themes/",
	"listing": "https://github.com/CatVinci-Studio/textus-plugins/blob/main/LISTING.md",
}

// runnable are the kinds of file that run when opened: a link to one is
// shown in its folder, never opened.
var runnable = strings.Fields(`exe msi bat cmd com scr pif cpl msc ps1 psm1 vbs vbe js jse wsf wsh hta reg lnk url jar
	sh bash zsh fish csh command tool app pkg run bin appimage desktop py pyw pl rb php scpt applescript workflow
	action terminal fileloc inetloc webloc dmg mpkg appref-ms jnlp chm msp settingcontent-ms library-ms
	searchconnector-ms appinstaller appx appxbundle msix msixbundle application inf wsc gadget xll iso img vhd vhdx
	diagcab mobileconfig prefpane saver kext deb rpm flatpakref`)

func isRunnable(path string) bool { return containsString(runnable, fileExt(path)) }

func markedToRun(path string) bool {
	if runtime.GOOS == "windows" || filepath.Ext(path) != "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func init() {
	command("open_web_link", func(c *Call, a struct {
		URL string `json:"url"`
	}) (nothing, error) {
		u, err := url.Parse(a.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "mailto") {
			return nil, errorf("not a web link")
		}
		return nil, mygo.Shell.OpenExternal(u.String())
	})
	command("open_about_link", func(c *Call, a struct {
		Page string `json:"page"`
	}) (nothing, error) {
		u, ok := aboutPages[a.Page]
		if !ok {
			return nil, errorf("no page %s", a.Page)
		}
		return nil, mygo.Shell.OpenExternal(u)
	})
	command("open_community_page", func(c *Call, a struct {
		Language *string `json:"language"`
	}) (nothing, error) {
		return nil, openCommunity(a.Language)
	})
	command("open_sponsor_page", func(c *Call, a struct {
		Language *string `json:"language"`
	}) (nothing, error) {
		return nil, openSponsor(a.Language)
	})
	command("open_tutorials_page", func(c *Call, a struct {
		Clip     *string `json:"clip"`
		Language *string `json:"language"`
	}) (nothing, error) {
		return nil, openTutorials(a.Clip, a.Language)
	})
	command("open_pandoc_install_page", func(c *Call, _ none) (nothing, error) {
		return nil, mygo.Shell.OpenExternal("https://pandoc.org/installing.html")
	})
	command("reveal_in_dir", func(c *Call, a struct {
		Path string `json:"path"`
	}) (nothing, error) {
		if err := requireNonEmpty(a.Path); err != nil {
			return nil, err
		}
		mygo.Shell.ShowItemInFolder(a.Path)
		return nil, nil
	})
	command("open_linked_file", func(c *Call, a struct {
		Path string `json:"path"`
	}) (nothing, error) {
		if err := requireNonEmpty(a.Path); err != nil {
			return nil, err
		}
		// What it is, not what it is called: a link named notes.txt may
		// lead to a script, and a file marked to run opens in a terminal.
		real, err := filepath.EvalSymlinks(a.Path)
		if err != nil {
			real = a.Path
		}
		info, statErr := os.Stat(real)
		if isRunnable(a.Path) || isRunnable(real) || markedToRun(real) || (statErr == nil && info.IsDir()) {
			mygo.Shell.ShowItemInFolder(a.Path)
			return nil, nil
		}
		return nil, mygo.Shell.OpenPath(a.Path)
	})
}
