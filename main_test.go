package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-switch/internal/app"
	"codex-switch/internal/store"
	"golang.org/x/term"
)

func TestHandleSwitchSuggestsLoginForUnknownAccount(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	application.Stdin = bytes.NewBufferString("\n")
	var stdout bytes.Buffer
	application.Stdout = &stdout

	err = handleSwitch(application, []string{"new-account"})
	if err == nil {
		t.Fatal("expected switch to fail for unknown account")
	}
	if !strings.Contains(err.Error(), `codex-switch switch --login "new-account"`) {
		t.Fatalf("expected login hint, got %q", err.Error())
	}
	if !strings.Contains(stdout.String(), `账号 "new-account" 还未保存`) {
		t.Fatalf("expected interactive prompt, got %q", stdout.String())
	}
}

func TestHandleSwitchCanStartLoginFlowAfterPrompt(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	application.Stdin = bytes.NewBufferString("y\n")
	var stdout bytes.Buffer
	application.Stdout = &stdout

	previousLogin := loginAccount
	called := false
	loginAccount = func(application *app.App, name string, force bool) (*store.Record, error) {
		called = true
		return &store.Record{
			Name:     name,
			Snapshot: store.Record{}.Snapshot,
		}, nil
	}
	defer func() {
		loginAccount = previousLogin
	}()

	if err := handleSwitch(application, []string{"new-account"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("expected prompt to continue into login flow")
	}
	if !strings.Contains(stdout.String(), `账号 "new-account" 还未保存`) {
		t.Fatalf("expected prompt output, got %q", stdout.String())
	}
}

func TestHandleSwitchCanCancelLoginFlowAfterPrompt(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	application.Stdin = bytes.NewBufferString("/cancel\n")
	var stdout bytes.Buffer
	application.Stdout = &stdout

	previousLogin := loginAccount
	called := false
	loginAccount = func(application *app.App, name string, force bool) (*store.Record, error) {
		called = true
		return nil, nil
	}
	defer func() {
		loginAccount = previousLogin
	}()

	err = handleSwitch(application, []string{"new-account"})
	if err == nil || !strings.Contains(err.Error(), "已取消登录") {
		t.Fatalf("expected login prompt cancellation, got %v", err)
	}
	if called {
		t.Fatal("expected cancellation to skip login flow")
	}
	if !strings.Contains(stdout.String(), "/cancel") || !strings.Contains(stdout.String(), "/exit") {
		t.Fatalf("expected prompt to mention cancel and exit, got %q", stdout.String())
	}
}

func TestHandleSwitchPromptsForSelectionWhenNoNameProvided(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildMainTestOAuthAuth(t, "Work", "work@example.com", "acct-work", "user-work"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("work", false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildMainTestOAuthAuth(t, "Personal", "personal@example.com", "acct-personal", "user-personal"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("personal", false); err != nil {
		t.Fatal(err)
	}

	application.Stdin = bytes.NewBufferString("personal\n")
	var stdout bytes.Buffer
	application.Stdout = &stdout

	previousSwitch := switchAccount
	selected := ""
	switchAccount = func(application *app.App, name string) (*store.Record, string, error) {
		selected = name
		return &store.Record{Name: name}, "", nil
	}
	defer func() {
		switchAccount = previousSwitch
	}()

	if err := handleSwitch(application, nil); err != nil {
		t.Fatal(err)
	}
	if selected != "personal" {
		t.Fatalf("expected interactive selection to choose personal, got %q", selected)
	}
	if !strings.Contains(stdout.String(), "请选择要切换的账号") {
		t.Fatalf("expected selection prompt, got %q", stdout.String())
	}
}

func TestHandleRemovePromptsForSelectionAndConfirmation(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildMainTestOAuthAuth(t, "Work", "work@example.com", "acct-work", "user-work"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("work", false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildMainTestOAuthAuth(t, "Personal", "personal@example.com", "acct-personal", "user-personal"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("personal", false); err != nil {
		t.Fatal(err)
	}

	application.Stdin = bytes.NewBufferString("personal\ny\n")
	var stdout bytes.Buffer
	application.Stdout = &stdout

	if err := handleRemove(application, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "请选择要删除的账号") {
		t.Fatalf("expected remove selection prompt, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `确认删除账号 "personal" 吗？`) {
		t.Fatalf("expected confirmation prompt, got %q", stdout.String())
	}
	if _, err := application.Store.Load("personal"); err == nil {
		t.Fatal("expected personal account to be deleted")
	}
	if _, err := application.Store.Load("work"); err != nil {
		t.Fatalf("expected work account to remain, got %v", err)
	}
}

func TestHandleRenamePromptsForSelectionAndNewName(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildMainTestOAuthAuth(t, "Work", "work@example.com", "acct-work", "user-work"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("work", false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildMainTestOAuthAuth(t, "Personal", "personal@example.com", "acct-personal", "user-personal"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("personal", false); err != nil {
		t.Fatal(err)
	}

	application.Stdin = bytes.NewBufferString("personal\nside-project\n")
	var stdout bytes.Buffer
	application.Stdout = &stdout

	if err := handleRename(application, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "请选择要重命名的账号") {
		t.Fatalf("expected rename selection prompt, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `请输入账号 "personal" 的新名称`) {
		t.Fatalf("expected rename text prompt, got %q", stdout.String())
	}
	record, err := application.Store.Load("side-project")
	if err != nil {
		t.Fatalf("expected renamed account to exist, got %v", err)
	}
	if record.Name != "side-project" {
		t.Fatalf("expected renamed record to use new name, got %q", record.Name)
	}
}

func TestHandleExportPromptsForSelectionAndPath(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildMainTestOAuthAuth(t, "Work", "work@example.com", "acct-work", "user-work"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("work", false); err != nil {
		t.Fatal(err)
	}

	application.Stdin = bytes.NewBufferString("work\nD:/backup/work-auth.json\n")
	var stdout bytes.Buffer
	application.Stdout = &stdout

	previousExport := exportAuthAccount
	selectedName := ""
	selectedPath := ""
	exportAuthAccount = func(application *app.App, name, targetPath string) (*store.Record, string, error) {
		selectedName = name
		selectedPath = targetPath
		return &store.Record{Name: name}, targetPath, nil
	}
	defer func() {
		exportAuthAccount = previousExport
	}()

	if err := handleExport(application, nil); err != nil {
		t.Fatal(err)
	}
	if selectedName != "work" || selectedPath != "D:/backup/work-auth.json" {
		t.Fatalf("unexpected export args: %q %q", selectedName, selectedPath)
	}
}

func TestHandleExportUsesDefaultPathWhenInputIsBlank(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildMainTestOAuthAuth(t, "Work", "work@example.com", "acct-work", "user-work"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("work", false); err != nil {
		t.Fatal(err)
	}

	application.Stdin = bytes.NewBufferString("work\n\n")
	var stdout bytes.Buffer
	application.Stdout = &stdout

	previousExport := exportAuthAccount
	selectedPath := ""
	exportAuthAccount = func(application *app.App, name, targetPath string) (*store.Record, string, error) {
		selectedPath = targetPath
		return &store.Record{Name: name}, targetPath, nil
	}
	defer func() {
		exportAuthAccount = previousExport
	}()

	if err := handleExport(application, nil); err != nil {
		t.Fatal(err)
	}
	want := defaultExportAuthPaths(application, "work")[0]
	if selectedPath != want {
		t.Fatalf("expected default export path %q, got %q", want, selectedPath)
	}
	if !strings.Contains(stdout.String(), absolutePathOrOriginal(want)) {
		t.Fatalf("expected export output to include absolute path, got %q", stdout.String())
	}
	if filepath.Dir(want) == "." {
		t.Fatalf("expected default export path to stay inside a directory, got %q", want)
	}
}

func TestHandleExportHomePromptsForSelectionAndDirectory(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildMainTestOAuthAuth(t, "Work", "work@example.com", "acct-work", "user-work"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("work", false); err != nil {
		t.Fatal(err)
	}

	application.Stdin = bytes.NewBufferString("work\nD:/CodexHomes/work\n")
	var stdout bytes.Buffer
	application.Stdout = &stdout

	previousExportHome := exportHomeAccount
	selectedName := ""
	selectedDir := ""
	exportHomeAccount = func(application *app.App, name, dir string, copyConfig bool) (*store.Record, string, error) {
		selectedName = name
		selectedDir = dir
		return &store.Record{Name: name}, dir, nil
	}
	defer func() {
		exportHomeAccount = previousExportHome
	}()

	if err := handleExportHome(application, nil); err != nil {
		t.Fatal(err)
	}
	if selectedName != "work" || selectedDir != "D:/CodexHomes/work" {
		t.Fatalf("unexpected export-home args: %q %q", selectedName, selectedDir)
	}
}

func TestHandleExportHomeCanSelectDefaultDirectoryByIndex(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildMainTestOAuthAuth(t, "Work", "work@example.com", "acct-work", "user-work"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("work", false); err != nil {
		t.Fatal(err)
	}

	application.Stdin = bytes.NewBufferString("work\n2\n")
	var stdout bytes.Buffer
	application.Stdout = &stdout

	previousExportHome := exportHomeAccount
	selectedDir := ""
	exportHomeAccount = func(application *app.App, name, dir string, copyConfig bool) (*store.Record, string, error) {
		selectedDir = dir
		return &store.Record{Name: name}, dir, nil
	}
	defer func() {
		exportHomeAccount = previousExportHome
	}()

	if err := handleExportHome(application, nil); err != nil {
		t.Fatal(err)
	}
	want := defaultExportHomePaths(application, "work")[1]
	if selectedDir != want {
		t.Fatalf("expected default export-home dir %q, got %q", want, selectedDir)
	}
}

func TestDefaultExportPathsStayInsideDirectories(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}

	authPaths := defaultExportAuthPaths(application, "work")
	if len(authPaths) == 0 {
		t.Fatal("expected default auth export paths")
	}
	for _, path := range authPaths {
		if filepath.Dir(path) == "." {
			t.Fatalf("expected auth export path to stay inside a directory, got %q", path)
		}
	}

	homePaths := defaultExportHomePaths(application, "work")
	if len(homePaths) == 0 {
		t.Fatal("expected default home export paths")
	}
	for _, path := range homePaths {
		if filepath.Dir(path) == "." {
			t.Fatalf("expected home export path to stay inside a directory, got %q", path)
		}
	}
}

func TestLocalFileURIUsesFileScheme(t *testing.T) {
	got := localFileURI(`C:\Users\Chen\cc-3-auth.json`)
	if !strings.HasPrefix(got, "file:///") {
		t.Fatalf("expected file URI, got %q", got)
	}
	if !strings.Contains(strings.ToLower(got), "cc-3-auth.json") {
		t.Fatalf("expected file URI to contain filename, got %q", got)
	}
}

func TestHandleRunPromptsForSelectionWhenNameMissing(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildMainTestOAuthAuth(t, "Work", "work@example.com", "acct-work", "user-work"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("work", false); err != nil {
		t.Fatal(err)
	}

	application.Stdin = bytes.NewBufferString("work\n")
	var stdout bytes.Buffer
	application.Stdout = &stdout

	previousRun := runAccount
	selectedName := ""
	runAccount = func(application *app.App, name string, opts app.RunOptions) (string, error) {
		selectedName = name
		return "D:/CodexHomes/work", nil
	}
	defer func() {
		runAccount = previousRun
	}()

	if err := handleRun(application, nil); err != nil {
		t.Fatal(err)
	}
	if selectedName != "work" {
		t.Fatalf("expected interactive run to select work, got %q", selectedName)
	}
}

func TestRunWithoutArgsStartsInteractiveShell(t *testing.T) {
	var stdout bytes.Buffer
	fakeApp := &app.App{
		Stdin:  bytes.NewBufferString("/exit\n"),
		Stdout: &stdout,
		Stderr: &stdout,
	}

	previousNewApp := newApplication
	newApplication = func() (*app.App, error) {
		return fakeApp, nil
	}
	defer func() {
		newApplication = previousNewApp
	}()

	if err := run(nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "codex-switch shell") {
		t.Fatalf("expected interactive shell banner, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Commands start with /. Type / to browse commands, /help for help, and /exit to quit.") {
		t.Fatalf("expected interactive shell hint, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Context: no-account") {
		t.Fatalf("expected interactive shell context, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "codex-switch[no-account]>") {
		t.Fatalf("expected interactive shell prompt, got %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "已退出 codex-switch") {
		t.Fatalf("expected interactive shell to exit quietly, got %q", stdout.String())
	}
}

func TestInteractiveShellRejectsBareCommand(t *testing.T) {
	var stdout bytes.Buffer
	fakeApp := &app.App{
		Stdin:  bytes.NewBufferString("help\n/exit\n"),
		Stdout: &stdout,
		Stderr: &stdout,
	}

	if err := runInteractiveShell(fakeApp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "错误: 交互命令需以 / 开头；输入 /help 查看命令") {
		t.Fatalf("expected slash-only shell hint, got %q", stdout.String())
	}
}

func TestInteractiveShellHelpShowsCommandTable(t *testing.T) {
	var stdout bytes.Buffer
	fakeApp := &app.App{
		Stdin:  bytes.NewBufferString("/help\n/exit\n"),
		Stdout: &stdout,
		Stderr: &stdout,
	}

	if err := runInteractiveShell(fakeApp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Available commands:") {
		t.Fatalf("expected shell command table, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "/help") || !strings.Contains(stdout.String(), "Tip: 输入 / 查看全部命令") {
		t.Fatalf("expected slash command table, got %q", stdout.String())
	}
}

func TestInteractiveShellSlashShowsCommandTable(t *testing.T) {
	var stdout bytes.Buffer
	fakeApp := &app.App{
		Stdin:  bytes.NewBufferString("/\n/exit\n"),
		Stdout: &stdout,
		Stderr: &stdout,
	}

	if err := runInteractiveShell(fakeApp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Available commands:") {
		t.Fatalf("expected slash shortcut to show command table, got %q", stdout.String())
	}
}

func TestInteractiveShellRejectsCommandPrefixOnEnter(t *testing.T) {
	var stdout bytes.Buffer
	fakeApp := &app.App{
		Stdin:  bytes.NewBufferString("/sw work\n/exit\n"),
		Stdout: &stdout,
		Stderr: &stdout,
	}

	if err := runInteractiveShell(fakeApp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `错误: 未知命令 "/sw"。输入 /help 查看可用命令`) {
		t.Fatalf("expected prefix command to be rejected, got %q", stdout.String())
	}
}

func TestInteractiveShellCanExitFromSelectionPrompt(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildMainTestOAuthAuth(t, "Work", "work@example.com", "acct-work", "user-work"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("work", false); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	application.Stdin = bytes.NewBufferString("/switch\n/exit\n")
	application.Stdout = &stdout
	application.Stderr = &stdout

	if err := runInteractiveShell(application); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "请选择要切换的账号") {
		t.Fatalf("expected selection prompt, got %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "错误: exit interactive shell") {
		t.Fatalf("expected prompt exit to stay quiet, got %q", stdout.String())
	}
}

func TestInteractiveShellRejectsAmbiguousCommandPrefixOnEnter(t *testing.T) {
	var stdout bytes.Buffer
	fakeApp := &app.App{
		Stdin:  bytes.NewBufferString("/r work\n/exit\n"),
		Stdout: &stdout,
		Stderr: &stdout,
	}

	if err := runInteractiveShell(fakeApp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `错误: 未知命令 "/r"。输入 /help 查看可用命令`) {
		t.Fatalf("expected ambiguous prefix command to be rejected, got %q", stdout.String())
	}
}

func TestInputReaderCachesBufferedReaderWithoutReplacingStdin(t *testing.T) {
	source := bytes.NewBufferString("first\nsecond\n")
	application := &app.App{Stdin: source}

	reader := inputReader(application)
	if reader == nil {
		t.Fatal("expected reader")
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if line != "first\n" {
		t.Fatalf("expected first line, got %q", line)
	}
	if application.Stdin != source {
		t.Fatal("expected application stdin to remain unchanged")
	}
	if application.InputReader != reader {
		t.Fatal("expected cached input reader")
	}

	readerAgain := inputReader(application)
	if readerAgain != reader {
		t.Fatal("expected inputReader to reuse cached reader")
	}
	line, err = readerAgain.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if line != "second\n" {
		t.Fatalf("expected second line, got %q", line)
	}
}

func TestSuspendRealtimeShellRawModeHandlesNestedSuspends(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "tty-*")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()

	originalState := &term.State{}
	rawState := &term.State{}
	restoreCalls := 0
	makeRawCalls := 0

	previousMakeRaw := termMakeRaw
	previousRestore := termRestore
	termMakeRaw = func(fd int) (*term.State, error) {
		makeRawCalls++
		return originalState, nil
	}
	termRestore = func(fd int, state *term.State) error {
		restoreCalls++
		if state != originalState {
			t.Fatalf("expected restore to use original state, got %p", state)
		}
		return nil
	}
	defer func() {
		termMakeRaw = previousMakeRaw
		termRestore = previousRestore
		activeRealtimeShellSession = nil
	}()

	activeRealtimeShellSession = &realtimeShellSession{
		input: input,
		state: originalState,
	}
	application := &app.App{Stdin: input}

	resumeOuter, err := suspendRealtimeShellRawMode(application)
	if err != nil {
		t.Fatal(err)
	}
	resumeInner, err := suspendRealtimeShellRawMode(application)
	if err != nil {
		t.Fatal(err)
	}
	if restoreCalls != 1 {
		t.Fatalf("expected one restore call for nested suspends, got %d", restoreCalls)
	}
	if activeRealtimeShellSession.suspendDepth != 2 {
		t.Fatalf("expected suspend depth 2, got %d", activeRealtimeShellSession.suspendDepth)
	}

	if err := resumeInner(); err != nil {
		t.Fatal(err)
	}
	if makeRawCalls != 0 {
		t.Fatalf("expected inner resume to skip MakeRaw, got %d calls", makeRawCalls)
	}
	if activeRealtimeShellSession.suspendDepth != 1 {
		t.Fatalf("expected suspend depth 1 after inner resume, got %d", activeRealtimeShellSession.suspendDepth)
	}

	activeRealtimeShellSession.state = rawState

	if err := resumeOuter(); err != nil {
		t.Fatal(err)
	}
	if makeRawCalls != 1 {
		t.Fatalf("expected outer resume to call MakeRaw once, got %d", makeRawCalls)
	}
	if activeRealtimeShellSession.suspendDepth != 0 {
		t.Fatalf("expected suspend depth 0 after outer resume, got %d", activeRealtimeShellSession.suspendDepth)
	}
	if activeRealtimeShellSession.state != originalState {
		t.Fatalf("expected final session state to stay original, got %p", activeRealtimeShellSession.state)
	}
}

func TestInteractiveShellUnknownSlashCommandShowsHint(t *testing.T) {
	var stdout bytes.Buffer
	fakeApp := &app.App{
		Stdin:  bytes.NewBufferString("/oops\n/exit\n"),
		Stdout: &stdout,
		Stderr: &stdout,
	}

	if err := runInteractiveShell(fakeApp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `错误: 未知命令 "/oops"。输入 /help 查看可用命令`) {
		t.Fatalf("expected unknown slash command hint, got %q", stdout.String())
	}
}

func TestInteractiveShellPromptShowsManagedAccount(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildMainTestOAuthAuth(t, "Work", "work@example.com", "acct-work", "user-work"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("work", false); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	application.Stdin = bytes.NewBufferString("/exit\n")
	application.Stdout = &stdout
	application.Stderr = &stdout

	if err := runInteractiveShell(application); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Context: work") {
		t.Fatalf("expected managed account context, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "codex-switch[work]>") {
		t.Fatalf("expected managed account prompt, got %q", stdout.String())
	}
}

func TestInteractiveShellPromptShowsUntrackedContext(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildMainTestOAuthAuth(t, "Work", "work@example.com", "acct-work", "user-work"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	application.Stdin = bytes.NewBufferString("/exit\n")
	application.Stdout = &stdout
	application.Stderr = &stdout

	if err := runInteractiveShell(application); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Context: untracked") {
		t.Fatalf("expected untracked context, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "codex-switch[untracked]>") {
		t.Fatalf("expected untracked prompt, got %q", stdout.String())
	}
}

func TestInteractiveShellDispatchesRenameCommandWithQuotes(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildMainTestOAuthAuth(t, "Jerry Butler", "jerry@example.com", "acct-jerry", "user-jerry"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("Jerry Butler", false); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	application.Stdin = bytes.NewBufferString(`/rename --from "Jerry Butler" --to jerry-butler` + "\n/exit\n")
	application.Stdout = &stdout
	application.Stderr = &stdout

	if err := runInteractiveShell(application); err != nil {
		t.Fatal(err)
	}
	if _, err := application.Store.Load("jerry-butler"); err != nil {
		t.Fatalf("expected rename command to rename account, got %v", err)
	}
}

func TestSplitCommandLineHandlesQuotedArgs(t *testing.T) {
	args, err := splitCommandLine(`rename --from "Jerry Butler" --to jerry-butler`)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, "|")
	want := "rename|--from|Jerry Butler|--to|jerry-butler"
	if got != want {
		t.Fatalf("unexpected parsed args: got %q want %q", got, want)
	}
}

func TestShellEnvSetCommandSupportsWindowsAndPosix(t *testing.T) {
	if got := shellEnvSetCommand("windows", "CODEX_HOME", `D:\CodexHomes\work`); got != `$env:CODEX_HOME="D:\CodexHomes\work"` {
		t.Fatalf("unexpected windows env command: %q", got)
	}
	if got := shellEnvSetCommand("linux", "CODEX_HOME", "/tmp/codex-work"); got != `export CODEX_HOME="/tmp/codex-work"` {
		t.Fatalf("unexpected linux env command: %q", got)
	}
	if got := shellEnvSetCommand("darwin", "CODEX_HOME", "/Users/me/.codex-work"); got != `export CODEX_HOME="/Users/me/.codex-work"` {
		t.Fatalf("unexpected darwin env command: %q", got)
	}
}

func TestInteractiveRealtimeSuggestionsForSlash(t *testing.T) {
	suggestions := interactiveRealtimeSuggestions(nil, "/")
	if len(suggestions.Matches) == 0 {
		t.Fatal("expected slash to show command suggestions")
	}
	if suggestions.Matches[0].Value != "/help" {
		t.Fatalf("expected first suggestion to be /help, got %q", suggestions.Matches[0].Value)
	}
}

func TestInteractiveRealtimeSuggestionsForBareCommand(t *testing.T) {
	suggestions := interactiveRealtimeSuggestions(nil, "help")
	if suggestions.Hint == "" {
		t.Fatal("expected bare command to show slash hint")
	}
}

func TestAutocompleteInteractiveLineUsesSelectedSuggestion(t *testing.T) {
	if got := autocompleteInteractiveLine(nil, "/sw", 0); got != "/switch " {
		t.Fatalf("unexpected autocomplete result: %q", got)
	}
	if got := autocompleteInteractiveLine(nil, "/r", 1); got != "/rename " {
		t.Fatalf("unexpected autocomplete with selection: %q", got)
	}
}

func TestApplySelectedSuggestionOnEnterHonorsArrowSelection(t *testing.T) {
	if got := string(applySelectedSuggestionOnEnter(nil, []byte("/r"), 1, true, true)); got != "/rename " {
		t.Fatalf("expected enter to accept selected suggestion, got %q", got)
	}
}

func TestApplySelectedSuggestionOnEnterDoesNotAutocompleteWithoutArrowSelection(t *testing.T) {
	if got := string(applySelectedSuggestionOnEnter(nil, []byte("/r"), 1, false, true)); got != "/r" {
		t.Fatalf("expected enter without arrow selection to keep input, got %q", got)
	}
}

func TestInteractiveExecutionLineUsesSelectedSuggestionWithoutTrailingSpace(t *testing.T) {
	if got := interactiveExecutionLine(nil, []byte("/r"), 1, true, true); got != "/rename" {
		t.Fatalf("expected execution line to show selected command, got %q", got)
	}
}

func TestInteractiveExecutionLineHidesSuggestionsForTypedInput(t *testing.T) {
	if got := interactiveExecutionLine(nil, []byte("/status"), -1, false, true); got != "/status" {
		t.Fatalf("expected execution line to preserve typed command, got %q", got)
	}
}

func TestRenderInteractiveSuggestionLinesDoesNotHighlightWithoutSelection(t *testing.T) {
	lines := renderInteractiveSuggestionLines(terminalUI{}, interactiveRealtimeSuggestions(nil, "/"), -1)
	for _, line := range lines {
		if strings.Contains(line, "\x1b[7m") {
			t.Fatalf("expected no highlighted suggestion without selection, got %q", line)
		}
	}
}

func TestMoveSuggestionSelectionWraps(t *testing.T) {
	if got := moveSuggestionSelection("/", 0, -1); got != len(interactiveShellCommands)-1 {
		t.Fatalf("expected selection to wrap to last command, got %d", got)
	}
	if got := moveSuggestionSelection("/r", 0, 1); got != 1 {
		t.Fatalf("expected selection to advance, got %d", got)
	}
}

func TestMoveSuggestionSelectionStartsFromEdgesWhenUnselected(t *testing.T) {
	if got := moveSuggestionSelection("/", -1, -1); got != len(interactiveShellCommands)-1 {
		t.Fatalf("expected first up from unselected to wrap to last command, got %d", got)
	}
	if got := moveSuggestionSelection("/", -1, 1); got != 0 {
		t.Fatalf("expected first down from unselected to select first command, got %d", got)
	}
}

func TestInteractivePathSuggestionsForUseSubcommand(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("CODEX_SWITCH_HOME", filepath.Join(tempDir, "store"))
	t.Setenv("CODEX_HOME", "")

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.Store.SavePathProfile("work", filepath.Join(tempDir, "work-home"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := application.Store.SavePathProfile("personal", filepath.Join(tempDir, "personal-home"), false); err != nil {
		t.Fatal(err)
	}

	suggestions := interactiveRealtimeSuggestions(application, "/paths use ")
	if len(suggestions.Matches) != 2 {
		t.Fatalf("expected path profile suggestions, got %+v", suggestions.Matches)
	}
	if suggestions.Matches[0].Group != "profiles" {
		t.Fatalf("expected profile suggestion group, got %+v", suggestions.Matches[0])
	}
	if suggestions.Matches[0].Value != "personal" && suggestions.Matches[1].Value != "work" {
		t.Fatalf("unexpected path suggestion values: %+v", suggestions.Matches)
	}
}

func TestInteractiveAccountSuggestionsForSwitch(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildMainTestOAuthAuth(t, "Work", "work@example.com", "acct-work", "user-work"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("Work Account", false); err != nil {
		t.Fatal(err)
	}

	suggestions := interactiveRealtimeSuggestions(application, "/switch ")
	if len(suggestions.Matches) == 0 {
		t.Fatal("expected account suggestions for /switch")
	}
	if suggestions.Matches[0].Value == "" {
		t.Fatalf("expected non-empty account suggestion: %+v", suggestions.Matches[0])
	}
	if suggestions.Matches[0].Group != "accounts" {
		t.Fatalf("expected account suggestion group: %+v", suggestions.Matches[0])
	}
}

func TestInteractiveRunSuggestionsShowParameterHintAfterAccount(t *testing.T) {
	tempDir := t.TempDir()
	activeHome := filepath.Join(tempDir, "active")
	storeHome := filepath.Join(tempDir, "store")

	t.Setenv("CODEX_HOME", activeHome)
	t.Setenv("CODEX_SWITCH_HOME", storeHome)

	if err := os.MkdirAll(activeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeHome, "auth.json"), buildMainTestOAuthAuth(t, "Work", "work@example.com", "acct-work", "user-work"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.AddCurrent("work", false); err != nil {
		t.Fatal(err)
	}

	suggestions := interactiveRealtimeSuggestions(application, "/run work ")
	if suggestions.Hint == "" {
		t.Fatalf("expected parameter hint after /run account, got %+v", suggestions)
	}
}

func buildMainTestOAuthAuth(t *testing.T, name, email, accountID, userID string) []byte {
	t.Helper()

	idClaims := map[string]any{
		"name":  name,
		"email": email,
		"sub":   userID,
		"exp":   float64(2208988800),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
			"chatgpt_user_id":    userID,
			"chatgpt_plan_type":  "plus",
		},
	}
	accessClaims := map[string]any{
		"sub": userID,
		"exp": float64(2208988800),
	}

	payload := map[string]any{
		"tokens": map[string]any{
			"id_token":      fakeMainTestJWT(t, idClaims),
			"access_token":  fakeMainTestJWT(t, accessClaims),
			"refresh_token": "refresh-" + userID,
			"account_id":    accountID,
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func fakeMainTestJWT(t *testing.T, payload map[string]any) string {
	t.Helper()

	header := `{"alg":"none","typ":"JWT"}`
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(header)) + "." +
		base64.RawURLEncoding.EncodeToString(body) + "."
}
