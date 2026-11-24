package backend

import (
	"os"
	"path/filepath"
	"runtime"
)

// Identifier names the app's own folders. It is the one the Tauri builds
// used, so a profile carries over between them.
const Identifier = "com.catvinci.textus"

// Dirs are the app's own folders: settings in Config, the rest of its data in
// Data, what may be rebuilt in Cache.
type Dirs struct {
	Config, Data, Cache string
}

// defaultDirs returns the folders Tauri's app_config_dir, app_data_dir and
// app_cache_dir named for Identifier.
func defaultDirs() (Dirs, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return Dirs{}, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return Dirs{}, err
	}
	data := config // macOS: Application Support; Windows: Roaming AppData
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		data = os.Getenv("XDG_DATA_HOME")
		if data == "" || !filepath.IsAbs(data) {
			home, err := os.UserHomeDir()
			if err != nil {
				return Dirs{}, err
			}
			data = filepath.Join(home, ".local", "share")
		}
	}
	return Dirs{
		Config: filepath.Join(config, Identifier),
		Data:   filepath.Join(data, Identifier),
		Cache:  filepath.Join(cache, Identifier),
	}, nil
}

// devBuild marks a development build, whose stores are files of their own
// ("settings.dev.json"), so it can run beside an installed Textus.
var devBuild bool

// fileName is the store file named stem: "<stem>.json", "<stem>.dev.json"
// in a development build.
func fileName(stem string) string { return dirName(stem) + ".json" }

// dirName is the folder named stem, "<stem>.dev" in a development build.
func dirName(stem string) string {
	if devBuild {
		return stem + ".dev"
	}
	return stem
}
