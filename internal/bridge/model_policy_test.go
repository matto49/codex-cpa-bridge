package bridge

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestModelPolicyPreviewApplyAndIdempotence(t *testing.T) {
	m := testManifest(t)
	m.Profiles.CPA.ModelCatalogJSON = filepath.Join(t.TempDir(), "models.json")
	before := `{"custom":{"keep":true},"models":[{"slug":"alpha","visibility":"list","priority":1},{"slug":"beta","visibility":"hide","priority":2}]}`
	if err := os.WriteFile(m.Profiles.CPA.ModelCatalogJSON, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := VisibilityPolicy{Models: []VisibilityPolicyEntry{{Slug: "alpha", Visibility: "hide"}, {Slug: "beta", Visibility: "list"}, {Slug: "remote-only", Visibility: "hide"}}}
	plan, err := PlanModelPolicy(m, policy)
	if err != nil || len(plan.Changes) != 2 || !reflect.DeepEqual(plan.Missing, []string{"remote-only"}) {
		t.Fatalf("unexpected plan: %+v %v", plan, err)
	}
	stillBefore, _ := os.ReadFile(m.Profiles.CPA.ModelCatalogJSON)
	if string(stillBefore) != before {
		t.Fatal("policy preview mutated catalog")
	}
	applied, err := ApplyModelPolicy(m, policy)
	if err != nil || !applied.Applied || applied.Backup == "" {
		t.Fatalf("policy apply: %+v %v", applied, err)
	}
	backup, _ := os.ReadFile(applied.Backup)
	if string(backup) != before {
		t.Fatal("policy backup does not contain previous catalog")
	}
	after, _ := os.ReadFile(m.Profiles.CPA.ModelCatalogJSON)
	if !strings.Contains(string(after), `"custom"`) {
		t.Fatal("policy lost unrelated catalog metadata")
	}
	second, err := ApplyModelPolicy(m, policy)
	if err != nil || second.Applied || len(second.Changes) != 0 {
		t.Fatalf("policy apply was not idempotent: %+v %v", second, err)
	}
}

func TestModelPolicyRejectsInvalidOrDuplicateEntries(t *testing.T) {
	m := testManifest(t)
	m.Profiles.CPA.ModelCatalogJSON = filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(m.Profiles.CPA.ModelCatalogJSON, []byte(`{"models":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []VisibilityPolicy{
		{Models: []VisibilityPolicyEntry{{Slug: "alpha", Visibility: "show"}}},
		{Models: []VisibilityPolicyEntry{{Slug: "alpha", Visibility: "hide"}, {Slug: "alpha", Visibility: "list"}}},
	} {
		if _, err := PlanModelPolicy(m, policy); err == nil {
			t.Fatalf("invalid policy accepted: %+v", policy)
		}
	}
}
