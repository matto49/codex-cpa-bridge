package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

type CatalogRefreshReport struct {
	Path      string   `json:"path"`
	CPAModels int      `json:"cpa_models"`
	Added     []string `json:"added"`
	Unchanged int      `json:"unchanged"`
	Backup    string   `json:"backup,omitempty"`
	Written   bool     `json:"written"`
}

type CatalogBootstrapReport struct {
	Path       string `json:"path"`
	CPAModels  int    `json:"cpa_models"`
	RichModels int    `json:"rich_models"`
	Action     string `json:"action"`
}

// BootstrapModelCatalog creates a Codex-compatible catalog only when the
// target does not exist. Unlike refresh, it needs complete model metadata;
// guessing metadata from an unrelated model would misrepresent capabilities.
func BootstrapModelCatalog(m Manifest, write bool) (CatalogBootstrapReport, error) {
	path := m.Profiles.CPA.ModelCatalogJSON
	if path == "" {
		return CatalogBootstrapReport{}, errors.New("profiles.cpa.model_catalog_json is not configured")
	}
	if _, err := os.Lstat(path); err == nil {
		return CatalogBootstrapReport{}, fmt.Errorf("model catalog already exists at %s; use models refresh instead", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return CatalogBootstrapReport{}, err
	}
	token, _ := loadAuthToken(m.Profiles.CPA)
	ok, status, body := httpJSON(m.Profiles.CPA.Endpoint+"/models", nil, token, 8*time.Second)
	if !ok {
		return CatalogBootstrapReport{}, fmt.Errorf("CPA /models returned %s; check endpoint and authentication", status)
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		return CatalogBootstrapReport{}, err
	}
	var advertised struct {
		Data []cpaModelEntry `json:"data"`
	}
	if err := json.Unmarshal(rawBody, &advertised); err != nil || len(advertised.Data) == 0 {
		return CatalogBootstrapReport{}, errors.New("CPA returned no usable model IDs")
	}
	const clientVersion = "0.154.0"
	richURL, err := url.Parse(m.Profiles.CPA.Endpoint + "/models")
	if err != nil {
		return CatalogBootstrapReport{}, err
	}
	query := richURL.Query()
	query.Set("client_version", clientVersion)
	richURL.RawQuery = query.Encode()
	var rich struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	if err := fetchCatalogJSON(richURL.String(), token, &rich); err != nil || len(rich.Models) == 0 {
		return CatalogBootstrapReport{}, errors.New("CPA does not provide the full Codex model metadata needed to bootstrap a catalog")
	}
	bySlug := make(map[string]map[string]json.RawMessage, len(rich.Models))
	for _, model := range rich.Models {
		if slug := rawString(model["slug"]); slug != "" {
			bySlug[slug] = model
		}
	}
	sort.Slice(advertised.Data, func(i, j int) bool { return advertised.Data[i].ID < advertised.Data[j].ID })
	models := make([]map[string]json.RawMessage, 0, len(advertised.Data))
	seen := make(map[string]bool, len(advertised.Data))
	for _, item := range advertised.Data {
		if item.ID == "" || seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		metadata, ok := bySlug[item.ID]
		if !ok {
			return CatalogBootstrapReport{}, fmt.Errorf("CPA did not provide full metadata for %q; catalog left untouched", item.ID)
		}
		for _, field := range []string{"context_window", "input_modalities", "supported_reasoning_levels", "default_reasoning_level", "priority"} {
			if len(metadata[field]) == 0 || bytes.Equal(metadata[field], []byte("null")) {
				return CatalogBootstrapReport{}, fmt.Errorf("CPA metadata for %q is missing %s; catalog left untouched", item.ID, field)
			}
		}
		model := cloneRawModel(metadata)
		model["visibility"] = jsonString("list")
		model["supported_in_api"] = json.RawMessage("true")
		if rawString(model["display_name"]) == "" {
			model["display_name"] = jsonString(displayModelName(item.ID))
		}
		models = append(models, model)
	}
	if len(models) == 0 {
		return CatalogBootstrapReport{}, errors.New("CPA returned no usable model metadata")
	}
	report := CatalogBootstrapReport{Path: path, CPAModels: len(models), RichModels: len(rich.Models), Action: "preview"}
	if !write {
		return report, nil
	}
	catalog := ModelCatalog{FetchedAt: jsonString(time.Now().UTC().Format(time.RFC3339)), ClientVersion: jsonString(clientVersion), Models: models}
	content, err := marshalModelCatalog(catalog)
	if err != nil {
		return report, err
	}
	if err := createPrivateFile(path, content); err != nil {
		return report, err
	}
	report.Action = "created"
	return report, nil
}

type cpaModelEntry struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by"`
}

// RefreshModelCatalog adds models advertised by CPA while preserving existing
// visibility and metadata. It does not delete or re-enable user-hidden models.
func RefreshModelCatalog(m Manifest) (CatalogRefreshReport, error) {
	path := m.Profiles.CPA.ModelCatalogJSON
	if path == "" {
		return CatalogRefreshReport{}, errors.New("profiles.cpa.model_catalog_json is not configured")
	}
	_, err := os.Stat(path)
	if err != nil {
		return CatalogRefreshReport{}, err
	}
	token, _ := loadAuthToken(m.Profiles.CPA)
	ok, status, body := httpJSON(m.Profiles.CPA.Endpoint+"/models", nil, token, 8*time.Second)
	if !ok {
		return CatalogRefreshReport{}, fmt.Errorf("CPA /models returned %s; check endpoint and authentication", status)
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		return CatalogRefreshReport{}, err
	}
	var source struct {
		Data []cpaModelEntry `json:"data"`
	}
	if err := json.Unmarshal(rawBody, &source); err != nil || len(source.Data) == 0 {
		return CatalogRefreshReport{}, errors.New("CPA returned no usable model IDs")
	}
	var rich []map[string]json.RawMessage
	if richURL, err := url.Parse(m.Profiles.CPA.Endpoint + "/models"); err == nil {
		query := richURL.Query()
		query.Set("client_version", "0.154.0")
		richURL.RawQuery = query.Encode()
		var parsed struct {
			Models []map[string]json.RawMessage `json:"models"`
		}
		if fetchCatalogJSON(richURL.String(), token, &parsed) == nil {
			rich = parsed.Models
		}
	}
	richBySlug := make(map[string]map[string]json.RawMessage, len(rich))
	for _, model := range rich {
		if slug := rawString(model["slug"]); slug != "" {
			richBySlug[slug] = model
		}
	}
	before, err := os.ReadFile(path)
	if err != nil {
		return CatalogRefreshReport{}, err
	}
	catalog, err := readModelCatalog(path)
	if err != nil {
		return CatalogRefreshReport{}, err
	}
	existing := make(map[string]bool, len(catalog.Models))
	maxPriority := 0
	for _, model := range catalog.Models {
		existing[rawString(model["slug"])] = true
		if priority := modelSummary(model).Priority; priority > maxPriority {
			maxPriority = priority
		}
	}
	report := CatalogRefreshReport{Path: path, CPAModels: len(source.Data), Added: []string{}}
	sort.Slice(source.Data, func(i, j int) bool { return source.Data[i].ID < source.Data[j].ID })
	for _, sourceModel := range source.Data {
		if sourceModel.ID == "" {
			continue
		}
		if existing[sourceModel.ID] {
			report.Unchanged++
			continue
		}
		seed := richBySlug[sourceModel.ID]
		if seed == nil {
			seed = catalogTemplateFor(sourceModel.ID, sourceModel.OwnedBy, catalog.Models, rich)
		}
		if seed == nil {
			return report, fmt.Errorf("CPA model %q has no metadata template; catalog left unchanged", sourceModel.ID)
		}
		model := cloneRawModel(seed)
		name := displayModelName(sourceModel.ID)
		model["slug"] = jsonString(sourceModel.ID)
		model["display_name"] = jsonString(name)
		model["description"] = jsonString(name)
		model["visibility"] = jsonString("list")
		model["supported_in_api"] = json.RawMessage("true")
		maxPriority++
		model["priority"] = json.RawMessage(fmt.Sprintf("%d", maxPriority))
		catalog.Models = append(catalog.Models, model)
		existing[sourceModel.ID] = true
		report.Added = append(report.Added, sourceModel.ID)
	}
	if len(report.Added) == 0 {
		return report, nil
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return report, err
	}
	if !bytes.Equal(current, before) {
		return report, errors.New("model catalog changed during refresh; retry")
	}
	content, err := marshalModelCatalog(catalog)
	if err != nil {
		return report, err
	}
	backup := fmt.Sprintf("%s.bak.%d", path, time.Now().UnixNano())
	if err := copyFile(path, backup); err != nil {
		return report, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return report, err
	}
	if err := atomicWrite(path, content, info.Mode().Perm()); err != nil {
		return report, err
	}
	report.Backup = backup
	report.Written = true
	return report, nil
}

func fetchCatalogJSON(endpoint, token string, target any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("catalog metadata returned HTTP %d", response.StatusCode)
	}
	const limit = 16 << 20
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(raw) > limit {
		return errors.New("catalog metadata exceeds 16 MiB or cannot be read")
	}
	return json.Unmarshal(raw, target)
}

func catalogTemplateFor(slug, owner string, existing, rich []map[string]json.RawMessage) map[string]json.RawMessage {
	family := modelFamily(slug, owner)
	for _, collection := range [][]map[string]json.RawMessage{rich, existing} {
		for _, model := range collection {
			if modelFamily(rawString(model["slug"]), "") == family && family != "" {
				return model
			}
		}
	}
	if len(rich) > 0 {
		return rich[0]
	}
	if len(existing) > 0 {
		return existing[0]
	}
	return nil
}

func modelFamily(slug, owner string) string {
	value := strings.ToLower(slug + " " + owner)
	for _, family := range []string{"gpt", "claude", "gemini", "kimi", "deepseek", "seed"} {
		if strings.Contains(value, family) {
			return family
		}
	}
	return ""
}

func cloneRawModel(source map[string]json.RawMessage) map[string]json.RawMessage {
	copy := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		copy[key] = append(json.RawMessage(nil), value...)
	}
	return copy
}

func jsonString(value string) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func displayModelName(slug string) string {
	if strings.HasPrefix(slug, "traex/") {
		return "Traex " + strings.TrimPrefix(slug, "traex/")
	}
	parts := strings.Split(slug, "-")
	for i, part := range parts {
		switch strings.ToLower(part) {
		case "gpt":
			parts[i] = "GPT"
		case "gemini":
			parts[i] = "Gemini"
		case "claude":
			parts[i] = "Claude"
		case "kimi":
			parts[i] = "Kimi"
		default:
			if part != "" && part[0] >= 'a' && part[0] <= 'z' {
				parts[i] = strings.ToUpper(part[:1]) + part[1:]
			}
		}
	}
	return strings.Join(parts, " ")
}
