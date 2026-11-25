package plugins

import (
	"log"
	"os"
	"testing"
)

// TestMain loads the permission list the app embeds (main.go), which the
// frontend shares.
func TestMain(m *testing.M) {
	raw, err := os.ReadFile("../../src/plugins/store/permissions.json")
	if err != nil {
		log.Fatal(err)
	}
	if err := LoadPermissions(raw); err != nil {
		log.Fatal(err)
	}
	os.Exit(m.Run())
}
