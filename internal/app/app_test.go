package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex-switch/internal/codex"
	"codex-switch/internal/store"
)

func TestAddCurrentAndSwitch(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}

	firstAuth := buildOAuthAuth(t, "Work Account", "work@example.com", "acct-work", "user-work", "plus")
	secondAuth := buildOAuthAuth(t, "Personal Account", "personal@example.com", "acct-personal", "user-personal", "team")

	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), firstAuth, 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := New()
	if err != nil {
		t.Fatal(err)
	}

	record, err := application.AddCurrent("work", false)
	if err != nil {
		t.Fatal(err)
	}
	if record.Snapshot.Email != "work@example.com" {
		t.Fatalf("unexpected email: %s", record.Snapshot.Email)
	}

	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), secondAuth, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("personal", false); err != nil {
		t.Fatal(err)
	}

	switched, backupPath, err := application.Switch("work")
	if err != nil {
		t.Fatal(err)
	}
	if switched.Name != "work" {
		t.Fatalf("unexpected switched account: %s", switched.Name)
	}
	if backupPath == "" {
		t.Fatal("expected backup path")
	}

	current, err := application.Current()
	if err != nil {
		t.Fatal(err)
	}
	if current.Managed == nil || current.Managed.Name != "work" {
		t.Fatalf("unexpected current managed account: %+v", current.Managed)
	}

	records, err := application.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(records))
	}

	st, err := store.New()
	if err != nil {
		t.Fatal(err)
	}
	if st.Root() != storeHome {
		t.Fatalf("unexpected store root: %s", st.Root())
	}
}

func TestSwitchPrefersCurrentWhenSameAccountIsNewer(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}

	olderAuth := buildOAuthAuthWithRefresh(t, "Work Account", "work@example.com", "acct-work", "user-work", "plus", "2026-01-01T00:00:00Z", 2208988800)
	newerAuth := buildOAuthAuthWithRefresh(t, "Work Account", "work@example.com", "acct-work", "user-work", "plus", "2026-02-01T00:00:00Z", 2211667200)

	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), olderAuth, 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := New()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := application.AddCurrent("work", false); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), newerAuth, 0o600); err != nil {
		t.Fatal(err)
	}

	record, backupPath, err := application.Switch("work")
	if err != nil {
		t.Fatal(err)
	}
	if backupPath != "" {
		t.Fatalf("expected no backup when keeping newer current auth, got %s", backupPath)
	}

	raw, err := os.ReadFile(filepath.Join(activeHome, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(newerAuth) {
		t.Fatal("expected current auth.json to remain the newer session")
	}
	if record.Snapshot.LastRefreshAt.IsZero() || record.Snapshot.LastRefreshAt.Format(time.RFC3339) != "2026-02-01T00:00:00Z" {
		t.Fatalf("expected stored record to refresh to latest auth, got %+v", record.Snapshot.LastRefreshAt)
	}
}

func TestSnapshotFromAPIKeyAuth(t *testing.T) {
	raw := []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-test","base_url":"https://api.example.com"}`)
	snapshot, err := codex.SnapshotFromRawAuth(raw)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.AuthMode != "apikey" {
		t.Fatalf("unexpected auth mode: %s", snapshot.AuthMode)
	}
	if snapshot.IdentityKey == "" {
		t.Fatal("expected identity key")
	}
}

func TestImportRenameAndExport(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}

	initialAuth := buildOAuthAuth(t, "Initial Account", "initial@example.com", "acct-initial", "user-initial", "free")
	importedAuth := buildOAuthAuth(t, "Imported Account", "imported@example.com", "acct-imported", "user-imported", "plus")
	importPath := filepath.Join(tempDir, "import-auth.json")

	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), initialAuth, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(importPath, importedAuth, 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := New()
	if err != nil {
		t.Fatal(err)
	}

	record, err := application.AddFromFile("imported", importPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if record.Snapshot.Email != "imported@example.com" {
		t.Fatalf("unexpected imported email: %s", record.Snapshot.Email)
	}

	record, err = application.Rename("imported", "renamed")
	if err != nil {
		t.Fatal(err)
	}
	if record.Name != "renamed" {
		t.Fatalf("unexpected renamed record: %s", record.Name)
	}

	exportPath := filepath.Join(tempDir, "exported-auth.json")
	if _, _, err := application.ExportAuth("renamed", exportPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(exportPath); err != nil {
		t.Fatal(err)
	}
}

func TestAddCurrentWithoutNameUsesCodexAccountName(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildOAuthAuth(t, "Jane Doe", "jane@example.com", "acct-1", "user-1", "plus"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := New()
	if err != nil {
		t.Fatal(err)
	}

	record, err := application.AddCurrent("", false)
	if err != nil {
		t.Fatal(err)
	}
	if record.Name != "jane-doe" {
		t.Fatalf("expected automatic account name, got %q", record.Name)
	}

	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildOAuthAuth(t, "Jane Doe", "jane2@example.com", "acct-2", "user-2", "plus"), 0o600); err != nil {
		t.Fatal(err)
	}

	record, err = application.AddCurrent("", false)
	if err != nil {
		t.Fatal(err)
	}
	if record.Name != "jane-doe-2" {
		t.Fatalf("expected deduplicated account name, got %q", record.Name)
	}
}

func TestImportWithoutNameUsesCodexAccountName(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")
	importPath := filepath.Join(tempDir, "import-auth.json")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(importPath, buildOAuthAuth(t, "Imported User", "imported@example.com", "acct-imported", "user-imported", "plus"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := New()
	if err != nil {
		t.Fatal(err)
	}

	record, err := application.AddFromFile("", importPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if record.Name != "imported-user" {
		t.Fatalf("expected imported record to use account name, got %q", record.Name)
	}
}

func TestStatusFetchesQuotaAndRefreshesStoredToken(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if got := r.Form.Get("refresh_token"); got != "refresh-user-1" {
				t.Fatalf("unexpected refresh token: %s", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"` + fakeJWT(t, map[string]any{
				"sub": "user-1",
				"exp": float64(2211667200),
				"https://api.openai.com/auth": map[string]any{
					"chatgpt_account_id": "acct-1",
				},
			}) + `","id_token":"` + fakeJWT(t, map[string]any{
				"name":  "Status User",
				"email": "status@example.com",
				"sub":   "user-1",
				"exp":   float64(2211667200),
				"https://api.openai.com/auth": map[string]any{
					"chatgpt_account_id": "acct-1",
					"chatgpt_user_id":    "user-1",
					"chatgpt_plan_type":  "plus",
				},
			}) + `"}`))
		case "/backend-api/wham/usage":
			if got := r.Header.Get("ChatGPT-Account-Id"); got != "acct-1" {
				t.Fatalf("unexpected account id header: %s", got)
			}
			if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
				t.Fatalf("missing authorization header: %s", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":25,"reset_at":2211668200},"secondary_window":{"used_percent":10,"reset_at":2212267200}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	restoreUsage := codex.SetQuotaUsageURLForTest(server.URL + "/backend-api/wham/usage")
	defer restoreUsage()
	restoreToken := codex.SetQuotaTokenURLForTest(server.URL + "/oauth/token")
	defer restoreToken()

	expiredAuth := buildOAuthAuthWithRefresh(t, "Status User", "status@example.com", "acct-1", "user-1", "plus", "", 946684800)
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), expiredAuth, 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("status-user", false); err != nil {
		t.Fatal(err)
	}

	statuses, err := application.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 {
		t.Fatalf("expected one status, got %d", len(statuses))
	}
	if statuses[0].Error != "" {
		t.Fatalf("unexpected status error: %s", statuses[0].Error)
	}
	if statuses[0].Quota == nil {
		t.Fatal("expected quota payload")
	}
	if statuses[0].Quota.Hourly.RemainingPercent != 75 || statuses[0].Quota.Weekly.RemainingPercent != 90 {
		t.Fatalf("unexpected quota percentages: %+v", statuses[0].Quota)
	}
	if !statuses[0].Refreshed {
		t.Fatal("expected token refresh to occur")
	}

	record, err := application.Store.Load("status-user")
	if err != nil {
		t.Fatal(err)
	}
	auth, err := codex.ParseAuthFile(record.RawAuth)
	if err != nil {
		t.Fatal(err)
	}
	if auth.Tokens == nil || auth.Tokens.AccessToken == "" {
		t.Fatalf("expected refreshed auth tokens, got %+v", auth.Tokens)
	}
	if auth.LastRefresh == nil {
		t.Fatal("expected last_refresh to be set after token refresh")
	}
}

func TestLoginRunsLogoutAndLoginThenSavesAccount(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildOAuthAuth(t, "Old Account", "old@example.com", "acct-old", "user-old", "plus"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := New()
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	application.Stdout = &stdout
	application.Stderr = &stderr

	restoreLookup, restoreRun := stubCodexCommands(t, activeHome, func(args []string, options commandIO) error {
		switch {
		case reflect.DeepEqual(args, []string{"logout"}):
			_ = os.Remove(filepath.Join(activeHome, "auth.json"))
			_, _ = options.Stdout.Write([]byte("logged out\n"))
			return nil
		case reflect.DeepEqual(args, []string{"login"}):
			_, _ = options.Stdout.Write([]byte("Opening browser for login...\n"))
			if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildOAuthAuth(t, "New Account", "new@example.com", "acct-new", "user-new", "pro"), 0o600); err != nil {
				t.Fatal(err)
			}
			return nil
		default:
			t.Fatalf("unexpected command args: %v", args)
			return nil
		}
	})
	defer restoreLookup()
	defer restoreRun()

	record, err := application.Login("new-account", false)
	if err != nil {
		t.Fatal(err)
	}
	if record.Name != "new-account" {
		t.Fatalf("unexpected record name: %s", record.Name)
	}
	if record.Snapshot.Email != "new@example.com" {
		t.Fatalf("unexpected email: %s", record.Snapshot.Email)
	}
	if got := stdout.String(); !strings.Contains(got, "正在打开浏览器进行 Codex 登录") {
		t.Fatalf("expected browser-login hint to be printed, got %q", got)
	}
	if got := stdout.String(); !strings.Contains(got, "Opening browser for login...") {
		t.Fatalf("expected raw login output to be forwarded, got %q", got)
	}

	saved, err := application.Store.Load("new-account")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Snapshot.AccountName != "New Account" {
		t.Fatalf("unexpected saved account name: %s", saved.Snapshot.AccountName)
	}
}

func TestLoginSkipsLogoutWhenNoCurrentAuth(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}

	application, err := New()
	if err != nil {
		t.Fatal(err)
	}

	var calls [][]string
	restoreLookup, restoreRun := stubCodexCommands(t, activeHome, func(args []string, options commandIO) error {
		copied := append([]string(nil), args...)
		calls = append(calls, copied)
		if reflect.DeepEqual(args, []string{"login"}) {
			if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildOAuthAuth(t, "Fresh Account", "fresh@example.com", "acct-fresh", "user-fresh", "plus"), 0o600); err != nil {
				t.Fatal(err)
			}
			return nil
		}
		t.Fatalf("unexpected command args: %v", args)
		return nil
	})
	defer restoreLookup()
	defer restoreRun()

	record, err := application.Login("", false)
	if err != nil {
		t.Fatal(err)
	}
	if record.Name != "fresh-account" {
		t.Fatalf("expected automatic name from login snapshot, got %q", record.Name)
	}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0], []string{"login"}) {
		t.Fatalf("expected only login command, got %v", calls)
	}
}

func TestSwitchAndRenameResolveShellFriendlyAliases(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}

	auth := buildOAuthAuth(t, "Jerry Butler", "jerry@example.com", "acct-jerry", "user-jerry", "plus")
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), auth, 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("Jerry Butler", false); err != nil {
		t.Fatal(err)
	}

	record, _, err := application.Switch("jerry-butler")
	if err != nil {
		t.Fatal(err)
	}
	if record.Name != "Jerry Butler" {
		t.Fatalf("unexpected record: %s", record.Name)
	}

	record, err = application.Rename("jerry-butler", "jerry")
	if err != nil {
		t.Fatal(err)
	}
	if record.Name != "jerry" {
		t.Fatalf("unexpected renamed record: %s", record.Name)
	}
}

func TestLoginFailsBeforeRunningCommandsWhenNameExists(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildOAuthAuth(t, "Existing Account", "existing@example.com", "acct-existing", "user-existing", "plus"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("existing", false); err != nil {
		t.Fatal(err)
	}

	restoreLookup, restoreRun := stubCodexCommands(t, activeHome, func(args []string, options commandIO) error {
		t.Fatalf("commands should not run when name already exists: %v", args)
		return nil
	})
	defer restoreLookup()
	defer restoreRun()

	if _, err := application.Login("existing", false); err == nil {
		t.Fatal("expected name conflict error")
	}
}

func buildOAuthAuth(t *testing.T, name, email, accountID, userID, plan string) []byte {
	return buildOAuthAuthWithRefresh(t, name, email, accountID, userID, plan, "", 2208988800)
}

func buildOAuthAuthWithRefresh(t *testing.T, name, email, accountID, userID, plan, lastRefresh string, exp int64) []byte {
	t.Helper()

	idClaims := map[string]any{
		"name":  name,
		"email": email,
		"sub":   userID,
		"exp":   float64(exp),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
			"chatgpt_user_id":    userID,
			"chatgpt_plan_type":  plan,
		},
	}
	accessClaims := map[string]any{
		"sub": userID,
		"exp": float64(exp),
	}

	payload := map[string]any{
		"tokens": map[string]any{
			"id_token":      fakeJWT(t, idClaims),
			"access_token":  fakeJWT(t, accessClaims),
			"refresh_token": "refresh-" + userID,
			"account_id":    accountID,
		},
	}
	if lastRefresh != "" {
		payload["last_refresh"] = lastRefresh
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func fakeJWT(t *testing.T, payload map[string]any) string {
	t.Helper()

	header := `{"alg":"none","typ":"JWT"}`
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(header)) + "." +
		base64.RawURLEncoding.EncodeToString(body) + "."
}

func stubCodexCommands(t *testing.T, activeHome string, runner func(args []string, options commandIO) error) (func(), func()) {
	t.Helper()

	previousLookup := lookupCommand
	previousRun := runCommand

	lookupCommand = func(file string) (string, error) {
		if file != "codex" {
			return "", errors.New("unexpected command lookup")
		}
		return filepath.Join(activeHome, "codex.exe"), nil
	}
	runCommand = func(name string, args []string, options commandIO) error {
		if !strings.EqualFold(filepath.Base(name), "codex.exe") {
			t.Fatalf("unexpected command path: %s", name)
		}
		return runner(args, options)
	}

	return func() {
			lookupCommand = previousLookup
		}, func() {
			runCommand = previousRun
		}
}
