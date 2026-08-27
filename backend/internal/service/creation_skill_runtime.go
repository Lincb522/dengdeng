package service

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

const creationSkillRuntimeVersion = 1

//go:embed creation_skill_packages.json
var creationSkillPackageAsset []byte

type creationSkillResource struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type creationSkillPackage struct {
	ID           string                  `json:"id"`
	Name         string                  `json:"name"`
	Description  string                  `json:"description"`
	Instructions string                  `json:"instructions"`
	References   []creationSkillResource `json:"references"`
	Scripts      []creationSkillResource `json:"scripts"`
	Revision     string                  `json:"revision"`
}

type creationSkillPackageCatalog struct {
	Version  int                    `json:"version"`
	Packages []creationSkillPackage `json:"packages"`
}

type CreationSkillRun struct {
	ID            string
	Revision      string
	Mode          string
	ResourceCount int
	Guidance      string
}

var (
	creationSkillPackagesOnce sync.Once
	creationSkillPackages     map[string]creationSkillPackage
	creationSkillPackagesErr  error
)

func loadCreationSkillPackages() (map[string]creationSkillPackage, error) {
	creationSkillPackagesOnce.Do(func() {
		var catalog creationSkillPackageCatalog
		if err := json.Unmarshal(creationSkillPackageAsset, &catalog); err != nil {
			creationSkillPackagesErr = fmt.Errorf("decode built-in skill packages: %w", err)
			return
		}
		if catalog.Version != creationSkillRuntimeVersion {
			creationSkillPackagesErr = fmt.Errorf("unsupported built-in skill package version %d", catalog.Version)
			return
		}
		creationSkillPackages = make(map[string]creationSkillPackage, len(catalog.Packages))
		for _, pkg := range catalog.Packages {
			if pkg.ID == "" || pkg.ID != pkg.Name || pkg.Instructions == "" || len(pkg.Revision) != 64 {
				creationSkillPackagesErr = fmt.Errorf("invalid built-in skill package %q", pkg.ID)
				return
			}
			if _, exists := creationSkillPackages[pkg.ID]; exists {
				creationSkillPackagesErr = fmt.Errorf("duplicate built-in skill package %q", pkg.ID)
				return
			}
			creationSkillPackages[pkg.ID] = pkg
		}
	})
	return creationSkillPackages, creationSkillPackagesErr
}

func ResolveCreationSkillRun(entry CreationLibraryEntry) (CreationSkillRun, error) {
	packages, err := loadCreationSkillPackages()
	if err != nil {
		return CreationSkillRun{}, err
	}
	if pkg, ok := packages[entry.ID]; ok && entry.SourceType == creationSourceBuiltin {
		return CreationSkillRun{
			ID:            entry.ID,
			Revision:      pkg.Revision,
			Mode:          "package",
			ResourceCount: len(pkg.References) + len(pkg.Scripts),
			Guidance:      renderCreationSkillPackage(pkg),
		}, nil
	}
	content := strings.TrimSpace(entry.Content)
	if content == "" {
		return CreationSkillRun{}, fmt.Errorf("skill %q has no executable instructions", entry.ID)
	}
	digest := sha256.Sum256([]byte(entry.ID + "\x00" + content))
	return CreationSkillRun{
		ID:       entry.ID,
		Revision: hex.EncodeToString(digest[:]),
		Mode:     "inline",
		Guidance: renderInlineCreationSkill(entry, content),
	}, nil
}

func renderCreationSkillPackage(pkg creationSkillPackage) string {
	var output strings.Builder
	output.WriteString("<dengdeng-skill id=\"")
	output.WriteString(pkg.ID)
	output.WriteString("\" revision=\"")
	output.WriteString(pkg.Revision)
	output.WriteString("\" mode=\"package\">\n")
	output.WriteString(strings.TrimSpace(pkg.Instructions))
	for _, reference := range pkg.References {
		output.WriteString("\n\n<skill-reference path=\"")
		output.WriteString(reference.Path)
		output.WriteString("\">\n")
		output.WriteString(strings.TrimSpace(reference.Content))
		output.WriteString("\n</skill-reference>")
	}
	for _, script := range pkg.Scripts {
		output.WriteString("\n\n<skill-script path=\"")
		output.WriteString(script.Path)
		output.WriteString("\">\n```text\n")
		output.WriteString(strings.TrimSpace(script.Content))
		output.WriteString("\n```\n</skill-script>")
	}
	output.WriteString("\n</dengdeng-skill>")
	return output.String()
}

func renderInlineCreationSkill(entry CreationLibraryEntry, content string) string {
	return fmt.Sprintf("<dengdeng-skill id=%q mode=\"inline\">\n%s\n</dengdeng-skill>", entry.ID, content)
}

func builtinCreationSkillPackageIDs() ([]string, error) {
	packages, err := loadCreationSkillPackages()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(packages))
	for id := range packages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
