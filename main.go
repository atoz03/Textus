// Command textus is the Textus desktop app: the Go side of a MyGo app
// (github.com/egoist/mygo) whose pages are the Vite build of src/.
package main

import (
	_ "embed"
	"encoding/json/v2"
	"log"
	"os"

	"textus/backend"
)

// The tables the frontend and the backend share, read where the frontend
// keeps them.
var (
	//go:embed package.json
	packageJSON []byte
	//go:embed src/prefs/backend-defaults.json
	backendDefaults []byte
	//go:embed src/prefs/appearance-tables.json
	appearanceTables []byte
	//go:embed src/theme/content-themes.css
	contentThemes string
	//go:embed src/registry/menu/menu.json
	menuTable []byte
	//go:embed src/i18n/errors.json
	errorTable []byte
	//go:embed src/registry/export-formats.json
	exportFormats []byte
	//go:embed src/editor/images/image-types.json
	imageTypes []byte
	//go:embed src/ai/ai-error-kinds.json
	errorKinds []byte
	//go:embed src/plugins/store/permissions.json
	pluginPermissions []byte
)

func main() {
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(packageJSON, &pkg); err != nil {
		log.Fatalf("textus: package.json: %v", err)
	}
	shared := backend.Shared{
		Version:          pkg.Version,
		BackendDefaults:  backendDefaults,
		AppearanceTables: appearanceTables,
		ContentThemes:    contentThemes,
		MenuTable:        menuTable,
		ErrorTable:       errorTable,
		ExportFormats:    exportFormats,
		ImageTypes:       imageTypes,
		ErrorKinds:       errorKinds,
		Permissions:      pluginPermissions,
	}
	// `textus help`, `textus search ...`: answered and gone before any
	// window.
	if code, ok := backend.RunCLI(shared); ok {
		os.Exit(code)
	}
	if err := backend.Run(shared); err != nil {
		log.Fatal(err)
	}
}
