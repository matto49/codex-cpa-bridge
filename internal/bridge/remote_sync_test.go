package bridge

import (
	"strings"
	"testing"
)

func TestRemotePreviewDistinguishesDriftFromUnavailable(t *testing.T) {
	policy := ModelPolicyReport{Missing: []string{"remote-unavailable"}}
	if detail := remotePreviewDetail(policy, nil); !strings.Contains(detail, "already matches") || !strings.Contains(detail, "1 source model") || !strings.Contains(detail, "not verified") {
		t.Fatalf("missing catalog entry was falsely called unavailable: %q", detail)
	}
	if detail := remotePreviewDetail(policy, &CatalogRefreshReport{Added: []string{"remote-unavailable"}}); !strings.Contains(detail, "advertises all") || !strings.Contains(detail, "refresh the catalog") {
		t.Fatalf("refreshable model was falsely called unavailable: %q", detail)
	}
	if detail := remotePreviewDetail(policy, &CatalogRefreshReport{}); !strings.Contains(detail, "does not advertise 1 source model") {
		t.Fatalf("verified unavailable model was not identified: %q", detail)
	}
	policy.Changes = []ModelPolicyChange{{Slug: "shared", From: "list", To: "hide"}}
	if detail := remotePreviewDetail(policy, &CatalogRefreshReport{}); !strings.Contains(detail, "apply 1 shared visibility change") || !strings.Contains(detail, "does not advertise") {
		t.Fatalf("misleading drift preview: %q", detail)
	}
	policy.Missing = []string{"one", "two"}
	if detail := remotePreviewDetail(policy, &CatalogRefreshReport{Added: []string{"one"}}); !strings.Contains(detail, "advertises 1 for refresh") || !strings.Contains(detail, "does not advertise 1") {
		t.Fatalf("mixed availability was not distinguished: %q", detail)
	}
	policy.Missing = nil
	policy.Changes = nil
	if detail := remotePreviewDetail(policy, nil); !strings.Contains(detail, "no write needed") {
		t.Fatalf("complete no-drift preview claimed a pending change: %q", detail)
	}
}

func TestRemoteSyncReportDistinguishesAppliedFromUnchangedPartial(t *testing.T) {
	unchanged := RemoteSyncReport{
		Action:      "applied_partial",
		RemoteReady: true,
		ModelPolicy: ModelPolicyReport{Missing: []string{"remote-unavailable"}},
	}
	finishRemoteSyncReport(&unchanged)
	if unchanged.Action != "unchanged_partial" || !strings.Contains(unchanged.Detail, "already matches") || !strings.Contains(unchanged.Detail, "1 source model remains") {
		t.Fatalf("no-op reported as applied: %+v", unchanged)
	}
	changed := unchanged
	changed.Action = "applied_partial"
	changed.ModelPolicy.Applied = true
	finishRemoteSyncReport(&changed)
	if changed.Action != "applied_partial" || !strings.Contains(changed.Detail, "Matching models synchronized") {
		t.Fatalf("applied change reported as no-op: %+v", changed)
	}
	complete := RemoteSyncReport{Action: "applied", RemoteReady: true}
	finishRemoteSyncReport(&complete)
	if complete.Action != "unchanged" || !strings.Contains(complete.Detail, "no changes applied") {
		t.Fatalf("no-op complete sync reported as applied: %+v", complete)
	}
	complete.RemoteReady = false
	finishRemoteSyncReport(&complete)
	if complete.Action != "needs_attention" || !strings.Contains(complete.Detail, "needs attention") {
		t.Fatalf("unready remote reported as synchronized: %+v", complete)
	}
	withUnlisted := RemoteSyncReport{
		Action:      "applied",
		RemoteReady: true,
		Platforms: PlatformSyncReport{Items: []PlatformSyncItem{
			{ID: "xbot", Action: "noop", Unlisted: []string{"xbot-only"}},
		}},
	}
	finishRemoteSyncReport(&withUnlisted)
	if withUnlisted.Action != "needs_attention" || !strings.Contains(withUnlisted.Detail, "outside the source catalog") {
		t.Fatalf("remote unlisted model reported fully synchronized: %+v", withUnlisted)
	}
}
