package service

import (
	"fmt"
	"strings"
	"testing"
)

func TestCreationLibraryDefaultsAndPublicView(t *testing.T) {
	settings := defaultCreationLibrarySettings()
	if !settings.Enabled || len(settings.Prompts) == 0 || len(settings.Rules) == 0 || len(settings.Skills) == 0 {
		t.Fatalf("incomplete defaults: %#v", settings)
	}
	settings.Prompts[0].Enabled = false
	public := PublicCreationLibrary(settings)
	if len(public.Prompts) != len(settings.Prompts)-1 {
		t.Fatalf("disabled prompt leaked into public view: %#v", public.Prompts)
	}
	if len(settings.Skills) < 100 {
		t.Fatalf("built-in skill catalog is too small: %d", len(settings.Skills))
	}
	sources := map[string]bool{}
	for _, skill := range settings.Skills {
		sources[skill.SourceType] = true
		if skill.NameEN == "" || skill.DescriptionEN == "" || skill.ContentEN == "" {
			t.Fatalf("skill lacks English text: %#v", skill)
		}
		if skill.SourceType == "official" || skill.SourceType == "community" {
			if skill.SourceURL == "" || skill.Author == "" {
				t.Fatalf("sourced skill lacks attribution: %#v", skill)
			}
		}
	}
	for _, kind := range []string{"builtin", "official", "community"} {
		if !sources[kind] {
			t.Fatalf("missing %s skills", kind)
		}
	}
}

func TestNormalizeCreationLibrary(t *testing.T) {
	library := CreationLibrarySettings{
		Enabled: true,
		Prompts: []CreationLibraryEntry{{ID: " First Prompt ", Name: " Prompt ", Content: " body ", Scope: "CHAT", Enabled: true, AutoApply: true}},
		Rules:   []CreationLibraryEntry{{ID: "rule", Name: "Rule", Content: "rule body", Scope: "", Enabled: true}},
	}
	if err := normalizeCreationLibrarySettings(&library); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if library.Prompts[0].ID != "first-prompt" || library.Prompts[0].Scope != CreationScopeChat || library.Prompts[0].AutoApply {
		t.Fatalf("prompt normalization = %#v", library.Prompts[0])
	}
	if library.Rules[0].Scope != CreationScopeAll {
		t.Fatalf("empty scope = %q", library.Rules[0].Scope)
	}
	if library.Rules[0].Author != "" || library.Rules[0].Category != "" || len(library.Rules[0].Tags) != 0 {
		t.Fatalf("custom rule metadata changed during upgrade: %#v", library.Rules[0])
	}

	library.Rules = append(library.Rules, library.Rules[0])
	if err := normalizeCreationLibrarySettings(&library); err == nil {
		t.Fatal("duplicate rule ID was accepted")
	}
}

func TestCreationLibraryUpgradeAddsCatalogWithoutOverwritingCustomEntry(t *testing.T) {
	library := CreationLibrarySettings{
		Enabled:        true,
		CatalogVersion: 2,
		Capabilities:   defaultCreationLibrarySettings().Capabilities,
		Skills: []CreationLibraryEntry{{
			ID: "code-reviewer", Name: "我的审查", Description: "自定义说明", Content: "自定义能力", Scope: CreationScopeChat,
			Enabled: true, Author: "站点管理员", SourceType: "custom",
		}},
	}
	if err := normalizeCreationLibrarySettings(&library); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if library.CatalogVersion != creationCatalogVersion || len(library.Skills) < 100 {
		t.Fatalf("catalog upgrade failed: version=%d skills=%d", library.CatalogVersion, len(library.Skills))
	}
	if library.Skills[0].Name != "我的审查" || library.Skills[0].Content != "自定义能力" || library.Skills[0].SourceType != "custom" || library.Skills[0].NameEN != "" {
		t.Fatalf("custom entry overwritten: %#v", library.Skills[0])
	}
}

func TestCreationLibraryUpgradeAddsEnglishToUnchangedBuiltInSkill(t *testing.T) {
	library := defaultCreationLibrarySettings()
	library.CatalogVersion = 4
	library.Skills[0].NameEN = ""
	library.Skills[0].DescriptionEN = ""
	library.Skills[0].ContentEN = ""
	if err := normalizeCreationLibrarySettings(&library); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if library.Skills[0].NameEN == "" || library.Skills[0].DescriptionEN == "" || library.Skills[0].ContentEN == "" {
		t.Fatalf("English text was not restored: %#v", library.Skills[0])
	}
}

func TestCreationLibraryAllowsCustomEntriesBeyondExpandedDefaults(t *testing.T) {
	library := defaultCreationLibrarySettings()
	for index := 0; index < 20; index++ {
		library.Skills = append(library.Skills, CreationLibraryEntry{
			ID: fmt.Sprintf("custom-skill-%d", index), Name: fmt.Sprintf("Custom %d", index), Content: "Custom guidance.",
			Scope: CreationScopeChat, Enabled: true, SourceType: "custom",
		})
	}
	if err := normalizeCreationLibrarySettings(&library); err != nil {
		t.Fatalf("expanded catalog rejected custom entries: %v", err)
	}
}

func TestCreationLibraryRejectsUnsafeSourceURL(t *testing.T) {
	library := defaultCreationLibrarySettings()
	library.Skills[0].SourceURL = "javascript:alert(1)"
	if err := normalizeCreationLibrarySettings(&library); err == nil {
		t.Fatal("unsafe source URL was accepted")
	}
}

func TestCreationLibraryPreservesSkillInstallCommand(t *testing.T) {
	library := defaultCreationLibrarySettings()
	library.Skills[0].InstallCommand = "  npx skills add example/review-skill  "
	if err := normalizeCreationLibrarySettings(&library); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if library.Skills[0].InstallCommand != "npx skills add example/review-skill" {
		t.Fatalf("install command = %q", library.Skills[0].InstallCommand)
	}
	library.Rules[0].InstallCommand = "should be removed"
	if err := normalizeCreationLibrarySettings(&library); err != nil {
		t.Fatalf("normalize rule: %v", err)
	}
	if library.Rules[0].InstallCommand != "" {
		t.Fatalf("rule install command was preserved: %q", library.Rules[0].InstallCommand)
	}
}

func TestCreationLibraryRejectsOversizedInstallCommand(t *testing.T) {
	library := defaultCreationLibrarySettings()
	library.Skills[0].InstallCommand = strings.Repeat("x", 2_001)
	if err := normalizeCreationLibrarySettings(&library); err == nil {
		t.Fatal("oversized install command was accepted")
	}
}

func TestSanitizeCreationSelection(t *testing.T) {
	library := defaultCreationLibrarySettings()
	selection, err := sanitizeCreationSelection(library, UserCreationSelection{
		RuleIDs: []string{"codex-engineering", "codex-engineering"}, SkillIDs: []string{"backend-engineer"},
	})
	if err != nil {
		t.Fatalf("sanitize selection: %v", err)
	}
	if len(selection.RuleIDs) != 1 || selection.RuleIDs[0] != "codex-engineering" || len(selection.SkillIDs) != 1 || selection.SkillIDs[0] != "backend-engineer" {
		t.Fatalf("selection = %#v", selection)
	}
	if _, err := sanitizeCreationSelection(library, UserCreationSelection{RuleIDs: []string{"codex-preserve-objective"}}); err == nil {
		t.Fatal("automatic rule was accepted as a manual selection")
	}
	library.Capabilities.Skills = false
	if _, err := sanitizeCreationSelection(library, UserCreationSelection{SkillIDs: []string{"backend-engineer"}}); err == nil {
		t.Fatal("disabled skill capability was accepted")
	}
}
