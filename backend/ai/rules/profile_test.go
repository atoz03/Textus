package rules

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestEveryOfferedProfileParsesToItsLanguage(t *testing.T) {
	for _, lp := range LanguageProfiles {
		for _, v := range lp.Profiles {
			p, ok := ParseProfile(v)
			if !ok {
				t.Fatalf("%s", v)
			}
			// zh-Hant-CN (GB rules, Traditional characters) is offered with
			// the mainland profiles.
			if v != "zh-Hant-CN" && p.Lang != lp.Lang {
				t.Errorf("%s: %s", v, p.Lang)
			}
		}
		if lp.Automatic != "" && !slices.Contains(lp.Profiles, lp.Automatic) {
			t.Errorf("%s: automatic %s", lp.Lang, lp.Automatic)
		}
	}
	p, _ := ParseProfile("zh-Hans-CN/fiction")
	if p.Region != "CN" || p.Preset != "fiction" {
		t.Errorf("%+v", p)
	}
	p, _ = ParseProfile("ja/koyobun")
	if p.Lang != Ja || p.Region != "" || !p.is("koyobun") {
		t.Errorf("%+v", p)
	}
	if _, ok := ParseProfile("xx-YY"); ok {
		t.Error("xx-YY")
	}
	if p, _ := ParseProfile("zh-Hant-CN"); !p.mainland() {
		t.Error("zh-Hant-CN is mainland")
	}
	if p, _ := ParseProfile("zh-Hant-TW"); p.mainland() {
		t.Error("zh-Hant-TW is not mainland")
	}
}

func TestTheFrontendListMirrorsThisOne(t *testing.T) {
	raw, err := os.ReadFile("../../../src/platform/ipc-ai-types.ts")
	if err != nil {
		t.Fatal(err)
	}
	ipc := string(raw)
	start := strings.Index(ipc, "export const WRITING_CONVENTIONS:")
	if start < 0 {
		t.Fatal("WRITING_CONVENTIONS missing in platform/ipc-ai-types.ts")
	}
	block := ipc[start : start+strings.Index(ipc[start:], "\n};")]
	for _, lp := range LanguageProfiles {
		code := string(lp.Lang)
		at := strings.Index(block, `"`+code+`":`)
		if at < 0 {
			at = strings.Index(block, "\n  "+code+":")
		}
		if at < 0 {
			t.Fatalf("%s missing in platform/ipc-ai-types.ts", code)
		}
		entry := block[at:]
		if e := strings.Index(entry, "},"); e >= 0 {
			entry = entry[:e+2]
		}
		if lp.Automatic != "" {
			if !strings.Contains(entry, `automatic: "`+lp.Automatic+`"`) {
				t.Errorf("%s", entry)
			}
		} else if !strings.Contains(entry, "automatic: null") {
			t.Errorf("%s", entry)
		}
		list := entry[strings.Index(entry, "profiles: ["):]
		list = list[:strings.Index(list, "]")]
		var listed []string
		for i, part := range strings.Split(list, `"`) {
			if i%2 == 1 {
				listed = append(listed, part)
			}
		}
		if !slices.Equal(listed, lp.Profiles) {
			t.Errorf("%s: frontend %v, backend %v", code, listed, lp.Profiles)
		}
	}
	// The setting the frontend keeps the profile under, by the same name.
	settings, err := os.ReadFile("../../../src/prefs/settings-types.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), "  "+SettingKey+": string;") {
		t.Errorf("%s is not in settings-types.ts", SettingKey)
	}
	if !strings.Contains(ipc, `WRITING_CONVENTIONS_AUTOMATIC = "`+Automatic+`"`) {
		t.Error("WRITING_CONVENTIONS_AUTOMATIC")
	}
}

func TestStyleFrontMatterSetsProfilesAndOptions(t *testing.T) {
	style := "---\nconventions: zh-Hans-CN/fiction, en-GB/oxford\ncjk-latin-space: on\nunknown: x\n  quotes: corner\n---\n# Voice\nShort sentences."
	pc := ProjectConventionsFromStyle(style)
	if !slices.Equal(pc.Profiles, []string{"zh-Hans-CN/fiction", "en-GB/oxford"}) {
		t.Errorf("%v", pc.Profiles)
	}
	// Indented and unknown keys are not options.
	if pc.Options["cjk-latin-space"] != "on" || len(pc.Options) != 1 {
		t.Errorf("%v", pc.Options)
	}
	if pc := ProjectConventionsFromStyle("# No front matter\nconventions: fr-CA"); len(pc.Profiles) != 0 || len(pc.Options) != 0 {
		t.Errorf("%+v", pc)
	}
}

func TestProjectBeatsSettingsBeatsAutomatic(t *testing.T) {
	c := defaultConv()
	auto := c.ProfileFor(ZhHans)
	if auto.ID != "zh-Hans-CN" || auto.Explicit || auto.Source != SourceAutomatic {
		t.Errorf("%+v", auto)
	}
	eqStr(t, c.ProfileFor(En).ID, "en")
	eqStr(t, c.ProfileFor(Pt).ID, "pt-BR")

	c.Setting = "automatic"
	if c.ProfileFor(Fr).Explicit {
		t.Error("automatic is no choice")
	}
	// The Settings row's single profile: its language only.
	c.Setting = "en-GB/govuk"
	if en := c.ProfileFor(En); en.ID != "en-GB/govuk" || en.Source != SourceSettings {
		t.Errorf("%+v", en)
	}
	if c.ProfileFor(ZhHans).Explicit {
		t.Error("an English choice for Chinese")
	}
	c.Setting = map[string]any{"zh": "zh-Hans-CN/tech", "fr": "fr-CA", "en": "automatic",
		"options": map[string]any{"quotes": "corner", "bogus": "x"}}
	zh := c.ProfileFor(ZhHans)
	if zh.ID != "zh-Hans-CN/tech" || zh.Source != SourceSettings || zh.opt("quotes") != "corner" {
		t.Errorf("%+v", zh)
	}
	if _, ok := zh.Options["bogus"]; ok {
		t.Error("bogus option kept")
	}
	eqStr(t, c.ProfileFor(Fr).ID, "fr-CA")
	if c.ProfileFor(En).Explicit {
		t.Error("en: automatic")
	}
	// A Traditional document doesn't take the Simplified choice.
	eqStr(t, c.ProfileFor(ZhHant).ID, "zh-Hant-TW")

	c.Project = ProjectConventionsFromStyle("---\nconventions: zh-Hans-CN/fiction\nquotes: curly\n---\n")
	zh = c.ProfileFor(ZhHans)
	if zh.ID != "zh-Hans-CN/fiction" || zh.Source != SourceProject || zh.opt("quotes") != "curly" {
		t.Errorf("%+v", zh)
	}
	// The project names no French profile: the setting still applies.
	if src := c.ProfileFor(Fr).Source; src != SourceSettings {
		t.Errorf("fr: %s", src)
	}
}
