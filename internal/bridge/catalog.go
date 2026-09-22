package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"
)

type ModelCatalog struct {
	FetchedAt     json.RawMessage              `json:"fetched_at,omitempty"`
	ClientVersion json.RawMessage              `json:"client_version,omitempty"`
	Models        []map[string]json.RawMessage `json:"models"`
	Extra         map[string]json.RawMessage   `json:"-"`
}

type ModelSummary struct {
	Slug           string `json:"slug"`
	DisplayName    string `json:"display_name"`
	Visibility     string `json:"visibility"`
	SupportedInAPI bool   `json:"supported_in_api"`
	Priority       int    `json:"priority"`
}

type ModelCatalogReport struct {
	Path   string         `json:"path"`
	Models []ModelSummary `json:"models"`
}

func LoadModelCatalog(m Manifest) (ModelCatalogReport, error) {
	path := m.Profiles.CPA.ModelCatalogJSON
	if path == "" {
		return ModelCatalogReport{}, errors.New("profiles.cpa.model_catalog_json is not configured")
	}
	catalog, err := readModelCatalog(path)
	if err != nil {
		return ModelCatalogReport{}, err
	}
	report := ModelCatalogReport{Path: path}
	for _, raw := range catalog.Models {
		report.Models = append(report.Models, modelSummary(raw))
	}
	sort.SliceStable(report.Models, func(i, j int) bool {
		if report.Models[i].Priority == report.Models[j].Priority {
			return report.Models[i].Slug < report.Models[j].Slug
		}
		return report.Models[i].Priority < report.Models[j].Priority
	})
	return report, nil
}

func SetModelVisibility(m Manifest, slug, visibility string) (ModelCatalogReport, error) {
	if slug == "" {
		return ModelCatalogReport{}, errors.New("model slug is required")
	}
	if visibility != "list" && visibility != "hide" {
		return ModelCatalogReport{}, errors.New("visibility must be list or hide")
	}
	path := m.Profiles.CPA.ModelCatalogJSON
	if path == "" {
		return ModelCatalogReport{}, errors.New("profiles.cpa.model_catalog_json is not configured")
	}
	catalog, err := readModelCatalog(path)
	if err != nil {
		return ModelCatalogReport{}, err
	}
	found := false
	for _, model := range catalog.Models {
		if rawString(model["slug"]) == slug {
			model["visibility"] = json.RawMessage(fmt.Sprintf("%q", visibility))
			found = true
			break
		}
	}
	if !found {
		return ModelCatalogReport{}, fmt.Errorf("model %q not found in %s", slug, path)
	}
	raw, err := marshalModelCatalog(catalog)
	if err != nil {
		return ModelCatalogReport{}, err
	}
	backup := fmt.Sprintf("%s.bak.%d", path, time.Now().UnixNano())
	if err := copyFile(path, backup); err != nil {
		return ModelCatalogReport{}, err
	}
	if err := atomicWrite(path, raw, 0o644); err != nil {
		return ModelCatalogReport{}, err
	}
	return LoadModelCatalog(m)
}

func readModelCatalog(path string) (ModelCatalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ModelCatalog{}, err
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return ModelCatalog{}, fmt.Errorf("invalid model catalog %s: %w", path, err)
	}
	var models []map[string]json.RawMessage
	if err := json.Unmarshal(root["models"], &models); err != nil {
		return ModelCatalog{}, fmt.Errorf("model catalog %s has invalid models: %w", path, err)
	}
	catalog := ModelCatalog{FetchedAt: root["fetched_at"], ClientVersion: root["client_version"], Models: models, Extra: map[string]json.RawMessage{}}
	for key, value := range root {
		if key != "fetched_at" && key != "client_version" && key != "models" {
			catalog.Extra[key] = value
		}
	}
	return catalog, nil
}

func marshalModelCatalog(catalog ModelCatalog) ([]byte, error) {
	root := make(map[string]any, len(catalog.Extra)+3)
	for key, value := range catalog.Extra {
		root[key] = value
	}
	if len(catalog.FetchedAt) > 0 {
		root["fetched_at"] = catalog.FetchedAt
	}
	if len(catalog.ClientVersion) > 0 {
		root["client_version"] = catalog.ClientVersion
	}
	root["models"] = catalog.Models
	raw, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func modelSummary(model map[string]json.RawMessage) ModelSummary {
	var supported bool
	var priority int
	_ = json.Unmarshal(model["supported_in_api"], &supported)
	_ = json.Unmarshal(model["priority"], &priority)
	return ModelSummary{
		Slug:           rawString(model["slug"]),
		DisplayName:    rawString(model["display_name"]),
		Visibility:     rawString(model["visibility"]),
		SupportedInAPI: supported,
		Priority:       priority,
	}
}

func rawString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
