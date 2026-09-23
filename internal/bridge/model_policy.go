package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

type VisibilityPolicyEntry struct {
	Slug       string `json:"slug"`
	Visibility string `json:"visibility"`
}

type VisibilityPolicy struct {
	Models []VisibilityPolicyEntry `json:"models"`
}

type ModelPolicyChange struct {
	Slug string `json:"slug"`
	From string `json:"from"`
	To   string `json:"to"`
}

type ModelPolicyReport struct {
	Path    string              `json:"path"`
	Changes []ModelPolicyChange `json:"changes"`
	Missing []string            `json:"missing,omitempty"`
	Backup  string              `json:"backup,omitempty"`
	Applied bool                `json:"applied"`
}

func ExportModelPolicy(m Manifest) (VisibilityPolicy, error) {
	catalog, err := LoadModelCatalog(m)
	if err != nil {
		return VisibilityPolicy{}, err
	}
	policy := VisibilityPolicy{Models: make([]VisibilityPolicyEntry, 0, len(catalog.Models))}
	for _, model := range catalog.Models {
		if model.Slug == "" || model.Visibility != "list" && model.Visibility != "hide" {
			return VisibilityPolicy{}, fmt.Errorf("catalog contains invalid model policy for %q", model.Slug)
		}
		policy.Models = append(policy.Models, VisibilityPolicyEntry{Slug: model.Slug, Visibility: model.Visibility})
	}
	return policy, nil
}

func PlanModelPolicy(m Manifest, policy VisibilityPolicy) (ModelPolicyReport, error) {
	report, _, _, err := planModelPolicy(m, policy)
	return report, err
}

func ApplyModelPolicy(m Manifest, policy VisibilityPolicy) (ModelPolicyReport, error) {
	report, before, after, err := planModelPolicy(m, policy)
	if err != nil || len(report.Changes) == 0 {
		return report, err
	}
	current, err := os.ReadFile(report.Path)
	if err != nil {
		return report, err
	}
	if !bytes.Equal(current, before) {
		return report, errors.New("model catalog changed since policy was planned; retry")
	}
	backup := fmt.Sprintf("%s.bak.%d", report.Path, time.Now().UnixNano())
	if err := copyFile(report.Path, backup); err != nil {
		return report, err
	}
	info, err := os.Stat(report.Path)
	if err != nil {
		return report, err
	}
	if err := atomicWrite(report.Path, after, info.Mode().Perm()); err != nil {
		return report, err
	}
	report.Applied = true
	report.Backup = backup
	return report, nil
}

func planModelPolicy(m Manifest, policy VisibilityPolicy) (ModelPolicyReport, []byte, []byte, error) {
	path := m.Profiles.CPA.ModelCatalogJSON
	report := ModelPolicyReport{Path: path, Changes: []ModelPolicyChange{}}
	if path == "" {
		return report, nil, nil, errors.New("profiles.cpa.model_catalog_json is not configured")
	}
	if len(policy.Models) == 0 {
		return report, nil, nil, errors.New("model visibility policy must not be empty")
	}
	wanted := make(map[string]string, len(policy.Models))
	for _, entry := range policy.Models {
		if entry.Slug == "" || entry.Visibility != "list" && entry.Visibility != "hide" {
			return report, nil, nil, fmt.Errorf("invalid policy entry for %q", entry.Slug)
		}
		if _, duplicate := wanted[entry.Slug]; duplicate {
			return report, nil, nil, fmt.Errorf("duplicate model policy for %q", entry.Slug)
		}
		wanted[entry.Slug] = entry.Visibility
	}
	before, err := os.ReadFile(path)
	if err != nil {
		return report, nil, nil, err
	}
	catalog, err := readModelCatalog(path)
	if err != nil {
		return report, nil, nil, err
	}
	seen := make(map[string]bool, len(catalog.Models))
	for _, model := range catalog.Models {
		slug := rawString(model["slug"])
		if visibility, exists := wanted[slug]; exists {
			seen[slug] = true
			current := rawString(model["visibility"])
			if current != visibility {
				report.Changes = append(report.Changes, ModelPolicyChange{Slug: slug, From: current, To: visibility})
				model["visibility"] = json.RawMessage(fmt.Sprintf("%q", visibility))
			}
		}
	}
	for _, entry := range policy.Models {
		if !seen[entry.Slug] {
			report.Missing = append(report.Missing, entry.Slug)
		}
	}
	if len(report.Changes) == 0 {
		return report, before, before, nil
	}
	after, err := marshalModelCatalog(catalog)
	return report, before, after, err
}
