package plugins

import (
	"encoding/json/v2"
	"os"
	"testing"
)

const goodManifest = `{"id":"a.b","name":"A","version":"1.0.0","publisher":"a",
        "engines":{"textus":"^1.3.0"}}`

func jsonOf(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func text(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// with is goodManifest with field set to value (JSON), or removed when
// value is "".
func with(t *testing.T, field, value string) string {
	t.Helper()
	m := jsonOf(t, goodManifest)
	if value == "" {
		delete(m, field)
	} else {
		var v any
		if err := json.Unmarshal([]byte(value), &v); err != nil {
			t.Fatal(err)
		}
		m[field] = v
	}
	return text(t, m)
}

func TestTheSharedCasesAreJudgedAsTheFrontendJudgesThem(t *testing.T) {
	raw, err := os.ReadFile("../../src/plugins/store/manifest-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		Base    map[string]any   `json:"base"`
		Valid   []map[string]any `json:"valid"`
		Invalid []map[string]any `json:"invalid"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	manifest := func(extra map[string]any) string {
		m := map[string]any{}
		for k, v := range cases.Base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return text(t, m)
	}
	for _, extra := range cases.Valid {
		if err := Check(manifest(extra)); err != nil {
			t.Errorf("refused %v: %v", extra, err)
		}
	}
	for _, extra := range cases.Invalid {
		if Check(manifest(extra)) == nil {
			t.Errorf("took %v", extra)
		}
	}
}

func TestAPermissionIsJudgedAsTheFrontendJudgesIt(t *testing.T) {
	raw, err := os.ReadFile("../../src/plugins/store/permissions.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		Hosts struct {
			Valid   []string `json:"valid"`
			Invalid []string `json:"invalid"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(lists.Plain) == 0 {
		t.Fatal("no permissions loaded")
	}
	for _, p := range append(append([]string{}, lists.Plain...), cases.Hosts.Valid...) {
		if !IsPermission(p) {
			t.Errorf("%s is refused", p)
		}
	}
	for _, p := range cases.Hosts.Invalid {
		if IsPermission(p) {
			t.Errorf("%s is taken", p)
		}
	}
}

func TestACompleteManifestIsAccepted(t *testing.T) {
	for _, m := range []string{
		goodManifest,
		with(t, "permissions", `["files.read","network:*.zotero.org:443"]`),
		with(t, "main", `"dist/host.js"`),
		with(t, "main", `["a.js","b.js"]`),
		with(t, "version", `"2.0.0-beta.1"`),
	} {
		if err := Check(m); err != nil {
			t.Errorf("%s: %v", m, err)
		}
	}
}

func TestAManifestMissingWhatItMustNameIsRefused(t *testing.T) {
	for _, field := range []string{"id", "version", "name", "publisher", "engines"} {
		if Check(with(t, field, "")) == nil {
			t.Errorf("no %s is taken", field)
		}
	}
	for _, m := range []string{"[]", "not json", with(t, "description", `""`)} {
		if Check(m) == nil {
			t.Errorf("%s is taken", m)
		}
	}
}

func TestIDsAndVersionsKeepTheirShape(t *testing.T) {
	for _, id := range []string{`"A.b"`, `"ab"`, `"../x.y"`, `"-a.b"`} {
		if Check(with(t, "id", id)) == nil {
			t.Errorf("id %s is taken", id)
		}
	}
	for _, v := range []string{`"1.0"`, `"1.0.0.0"`, `"v1.0.0"`, `"1.0.0-"`, `"1.0.0-a b"`} {
		if Check(with(t, "version", v)) == nil {
			t.Errorf("version %s is taken", v)
		}
	}
}

func TestEmbedsNeedFilesRead(t *testing.T) {
	embeds := func(permissions string) error {
		m := jsonOf(t, goodManifest)
		m["contributes"] = map[string]any{"embeds": []any{map[string]any{"kind": "x"}}}
		var p any
		if err := json.Unmarshal([]byte(permissions), &p); err != nil {
			t.Fatal(err)
		}
		m["permissions"] = p
		return Check(text(t, m))
	}
	if err := embeds(`[]`); err == nil || err.Error() != "contributes.embeds needs files.read." {
		t.Errorf("no files.read: %v", err)
	}
	// Writing includes reading, as everywhere else.
	for _, p := range []string{`["files.read"]`, `["files.write"]`} {
		if err := embeds(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestOnlyPermissionsTextusHasCanBeAskedFor(t *testing.T) {
	for _, p := range []string{"files.read", "background", "network:*", "network:api.x.org", "network:*.x.org", "network:localhost:8080"} {
		if !IsPermission(p) {
			t.Errorf("%s is refused", p)
		}
	}
	for _, p := range []string{"root", "files.*", "network:", "network:x.org/path", "network:x.org:", "network:x.org:123456", "network:**.x.org"} {
		if IsPermission(p) {
			t.Errorf("%s is taken", p)
		}
	}
	for _, v := range []string{`["shell"]`, `[1]`, `"files.read"`} {
		if Check(with(t, "permissions", v)) == nil {
			t.Errorf("permissions %s are taken", v)
		}
	}
}

func TestBackgroundWorkStaysOnTheDesktop(t *testing.T) {
	if err := Check(with(t, "platforms", `["desktop","ios"]`)); err != nil {
		t.Error(err)
	}
	if Check(with(t, "platforms", `["android"]`)) == nil {
		t.Error("android is taken")
	}
	background := with(t, "background", `"jobs.js"`)
	if err := Check(background); err != nil {
		t.Error(err)
	}
	m := jsonOf(t, background)
	m["platforms"] = []any{"ios"}
	if Check(text(t, m)) == nil {
		t.Error("background work on iOS is taken")
	}
	m = jsonOf(t, goodManifest)
	m["requires"] = []any{map[string]any{"setting": "runInBackground"}}
	m["platforms"] = []any{"desktop", "ios"}
	if err := Check(text(t, m)); err != nil {
		t.Error(err)
	}
	if Check(with(t, "requires", `[{"setting":"sudo"}]`)) == nil {
		t.Error("requiring sudo is taken")
	}
}
