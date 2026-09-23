package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// xbot v0.0.52 exposes model controls through a login-session-bound Web RPC.
// The local database adapter is deliberately schema-gated and modifies only
// subscription and model rows belonging to the selected CPA URL.
type xbotSubscription struct {
	ID      string `json:"id"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
}

type xbotDefaultModel struct {
	SenderID       string `json:"sender_id"`
	SubscriptionID string `json:"subscription_id"`
	Model          string `json:"model"`
}

type xbotModel struct {
	SubscriptionID string `json:"subscription_id"`
	Model          string `json:"model"`
	Enabled        int    `json:"enabled"`
}

type xbotDBSnapshot struct {
	subscriptions []xbotSubscription
	models        []xbotModel
	defaults      []xbotDefaultModel
}

type xbotDBAction struct {
	subscriptionID string
	baseURL        string
	model          string
	oldEnabled     int // -1 means absent
	newEnabled     int
}

type xbotDBChange struct {
	path         string
	actions      []xbotDBAction
	defaultEdits []xbotDefaultEdit
}

type xbotDefaultEdit struct {
	subscriptionID string
	senderID       string // empty for subscription-level preferred model
	previous       string
	next           string
}

func planXbotDB(m Manifest, catalog []ModelSummary) (PlatformSyncItem, *xbotDBChange) {
	item := PlatformSyncItem{ID: "xbot", Path: m.Platforms.XbotDatabase, Action: "blocked"}
	snapshot, err := readXbotDB(m.Platforms.XbotDatabase, m.Profiles.CPA.Endpoint)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, configErr := os.Stat(m.Platforms.XbotConfigJSON); errors.Is(configErr, os.ErrNotExist) {
				item.Action, item.Detail = "skipped", "xbot is not installed on this host"
				return item, nil
			}
		}
		item.Detail = "Cannot inspect xbot database: " + err.Error()
		return item, nil
	}
	if len(snapshot.subscriptions) == 0 {
		item.Action, item.Detail = "skipped", "No xbot subscription points to this CPA endpoint"
		return item, nil
	}
	policy := make(map[string]bool, len(catalog))
	firstVisible := ""
	for _, model := range catalog {
		if model.Slug != "" {
			policy[model.Slug] = model.Visibility == "list"
			if model.Visibility == "list" && firstVisible == "" {
				firstVisible = model.Slug
			}
		}
	}
	bySubscription := make(map[string]map[string]xbotModel, len(snapshot.subscriptions))
	for _, row := range snapshot.models {
		if bySubscription[row.SubscriptionID] == nil {
			bySubscription[row.SubscriptionID] = make(map[string]xbotModel)
		}
		bySubscription[row.SubscriptionID][row.Model] = row
	}
	var actions []xbotDBAction
	var defaultEdits []xbotDefaultEdit
	for _, sub := range snapshot.subscriptions {
		if visible, known := policy[sub.Model]; known && !visible {
			if firstVisible == "" {
				item.Detail = "Cannot hide xbot's preferred model when no visible replacement exists"
				return item, nil
			}
			defaultEdits = append(defaultEdits, xbotDefaultEdit{sub.ID, "", sub.Model, firstVisible})
		}
		for _, selected := range snapshot.defaults {
			if selected.SubscriptionID != sub.ID {
				continue
			}
			if visible, known := policy[selected.Model]; known && !visible {
				if firstVisible == "" {
					item.Detail = "Cannot hide xbot's selected model when no visible replacement exists"
					return item, nil
				}
				defaultEdits = append(defaultEdits, xbotDefaultEdit{sub.ID, selected.SenderID, selected.Model, firstVisible})
			}
		}
		for _, model := range catalog {
			if model.Slug == "" {
				continue
			}
			want := 0
			if policy[model.Slug] {
				want = 1
			}
			row, exists := bySubscription[sub.ID][model.Slug]
			if exists && row.Enabled == want || !exists && want == 0 {
				continue
			}
			old := -1
			if exists {
				old = row.Enabled
			}
			actions = append(actions, xbotDBAction{sub.ID, sub.BaseURL, model.Slug, old, want})
			label := model.Slug
			if len(snapshot.subscriptions) > 1 {
				label = sub.ID + "/" + model.Slug
			}
			if want == 1 {
				item.Add = append(item.Add, label)
			} else {
				item.Remove = append(item.Remove, label)
			}
		}
	}
	item.RestartNeeded = true
	if len(actions) == 0 && len(defaultEdits) == 0 {
		item.Action, item.Detail, item.RestartNeeded = "noop", "xbot subscription model flags already match the catalog", false
		return item, nil
	}
	item.Action, item.Detail = "update", fmt.Sprintf("Update %d xbot model flags and %d preferred selections across %d CPA subscription(s); back up first, then refresh or reconnect xbot", len(actions), len(defaultEdits), len(snapshot.subscriptions))
	return item, &xbotDBChange{path: m.Platforms.XbotDatabase, actions: actions, defaultEdits: defaultEdits}
}

func readXbotDB(path, endpoint string) (xbotDBSnapshot, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return xbotDBSnapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return xbotDBSnapshot{}, errors.New("database must be a regular file")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return xbotDBSnapshot{}, errors.New("sqlite3 CLI is required for the xbot adapter")
	}
	for table, required := range map[string][]string{
		"user_llm_subscriptions": {"id", "base_url", "model"},
		"subscription_models":    {"id", "subscription_id", "model", "enabled", "updated_at"},
		"user_default_model":     {"sender_id", "subscription_id", "model", "updated_at"},
	} {
		var columns []struct {
			Name string `json:"name"`
		}
		if err := xbotQuery(path, "SELECT name FROM pragma_table_info("+sqlLiteral(table)+")", &columns); err != nil {
			return xbotDBSnapshot{}, err
		}
		for _, column := range required {
			found := false
			for _, present := range columns {
				if present.Name == column {
					found = true
					break
				}
			}
			if !found {
				return xbotDBSnapshot{}, fmt.Errorf("unsupported xbot schema: %s.%s is missing", table, column)
			}
		}
	}
	var all []xbotSubscription
	if err := xbotQuery(path, "SELECT id, base_url, model FROM user_llm_subscriptions ORDER BY id", &all); err != nil {
		return xbotDBSnapshot{}, err
	}
	matching := make(map[string]struct{})
	snapshot := xbotDBSnapshot{}
	for _, sub := range all {
		if sameEndpoint(sub.BaseURL, endpoint) {
			snapshot.subscriptions = append(snapshot.subscriptions, sub)
			matching[sub.ID] = struct{}{}
		}
	}
	if len(matching) == 0 {
		return snapshot, nil
	}
	var rows []xbotModel
	if err := xbotQuery(path, "SELECT subscription_id, model, enabled FROM subscription_models ORDER BY subscription_id, model", &rows); err != nil {
		return xbotDBSnapshot{}, err
	}
	for _, row := range rows {
		if _, ok := matching[row.SubscriptionID]; ok {
			if row.Enabled != 0 && row.Enabled != 1 {
				return xbotDBSnapshot{}, errors.New("unsupported xbot schema: model enabled flag is not boolean")
			}
			snapshot.models = append(snapshot.models, row)
		}
	}
	var defaults []xbotDefaultModel
	if err := xbotQuery(path, "SELECT sender_id, subscription_id, model FROM user_default_model ORDER BY sender_id", &defaults); err != nil {
		return xbotDBSnapshot{}, err
	}
	for _, row := range defaults {
		if _, ok := matching[row.SubscriptionID]; ok {
			snapshot.defaults = append(snapshot.defaults, row)
		}
	}
	return snapshot, nil
}

func xbotQuery(path, query string, target any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "sqlite3", "-readonly", "-json", path, query).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sqlite3 read failed: %w", err)
	}
	if len(strings.TrimSpace(string(output))) == 0 {
		output = []byte("[]")
	}
	if err := json.Unmarshal(output, target); err != nil {
		return errors.New("sqlite3 returned invalid JSON")
	}
	return nil
}

func applyXbotDBChange(change xbotDBChange) (string, error) {
	if len(change.actions) == 0 && len(change.defaultEdits) == 0 {
		return "", nil
	}
	info, err := os.Lstat(change.path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("xbot database is no longer a regular file")
	}
	backup := fmt.Sprintf("%s.bak.%d", change.path, time.Now().UnixNano())
	f, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := exec.CommandContext(ctx, "sqlite3", "-bail", change.path, ".backup "+strconv.Quote(backup)).CombinedOutput(); err != nil {
		_ = os.Remove(backup)
		return backup, fmt.Errorf("xbot online backup failed: %w", err)
	}

	var sql strings.Builder
	sql.WriteString("PRAGMA busy_timeout=5000; BEGIN IMMEDIATE; CREATE TEMP TABLE bridge_assertion(ok INTEGER NOT NULL CHECK(ok=1));")
	sort.Slice(change.actions, func(i, j int) bool {
		if change.actions[i].subscriptionID == change.actions[j].subscriptionID {
			return change.actions[i].model < change.actions[j].model
		}
		return change.actions[i].subscriptionID < change.actions[j].subscriptionID
	})
	for _, action := range change.actions {
		subID := sqlLiteral(action.subscriptionID)
		model := sqlLiteral(action.model)
		fmt.Fprintf(&sql, "INSERT INTO bridge_assertion SELECT CASE WHEN EXISTS(SELECT 1 FROM user_llm_subscriptions WHERE id=%s AND base_url=%s) THEN 1 ELSE 0 END;", subID, sqlLiteral(action.baseURL))
		if action.oldEnabled == -1 {
			fmt.Fprintf(&sql, "INSERT INTO bridge_assertion SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM subscription_models WHERE subscription_id=%s AND model=%s) THEN 1 ELSE 0 END;", subID, model)
			id, err := randomXbotID()
			if err != nil {
				return backup, err
			}
			fmt.Fprintf(&sql, "INSERT INTO subscription_models(id,subscription_id,model,enabled) VALUES(%s,%s,%s,%d);", sqlLiteral(id), subID, model, action.newEnabled)
		} else {
			fmt.Fprintf(&sql, "INSERT INTO bridge_assertion SELECT CASE WHEN EXISTS(SELECT 1 FROM subscription_models WHERE subscription_id=%s AND model=%s AND enabled=%d) THEN 1 ELSE 0 END;", subID, model, action.oldEnabled)
			fmt.Fprintf(&sql, "UPDATE subscription_models SET enabled=%d, updated_at=datetime('now') WHERE subscription_id=%s AND model=%s;", action.newEnabled, subID, model)
		}
	}
	for _, edit := range change.defaultEdits {
		subID := sqlLiteral(edit.subscriptionID)
		oldModel := sqlLiteral(edit.previous)
		newModel := sqlLiteral(edit.next)
		if edit.senderID == "" {
			fmt.Fprintf(&sql, "INSERT INTO bridge_assertion SELECT CASE WHEN EXISTS(SELECT 1 FROM user_llm_subscriptions WHERE id=%s AND model=%s) THEN 1 ELSE 0 END;", subID, oldModel)
			fmt.Fprintf(&sql, "UPDATE user_llm_subscriptions SET model=%s WHERE id=%s;", newModel, subID)
		} else {
			senderID := sqlLiteral(edit.senderID)
			fmt.Fprintf(&sql, "INSERT INTO bridge_assertion SELECT CASE WHEN EXISTS(SELECT 1 FROM user_default_model WHERE sender_id=%s AND subscription_id=%s AND model=%s) THEN 1 ELSE 0 END;", senderID, subID, oldModel)
			fmt.Fprintf(&sql, "UPDATE user_default_model SET model=%s,updated_at=datetime('now') WHERE sender_id=%s;", newModel, senderID)
		}
	}
	sql.WriteString("COMMIT;")
	if _, err := exec.CommandContext(ctx, "sqlite3", "-bail", change.path, sql.String()).CombinedOutput(); err != nil {
		return backup, fmt.Errorf("xbot database changed or write failed: %w; pre-write backup: %s", err, backup)
	}
	return backup, nil
}

func randomXbotID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "bridge-" + hex.EncodeToString(raw[:]), nil
}

func sqlLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
