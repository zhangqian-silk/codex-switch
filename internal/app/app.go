package app

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"codex-switch/internal/codex"
	"codex-switch/internal/store"
)

type App struct {
	Store             *store.Store
	ActiveHome        string
	ActiveAuthPath    string
	ActiveConfigPath  string
	ActivePathSource  string
	ActivePathProfile string
	Stdin             io.Reader
	InputReader       *bufio.Reader
	Stdout            io.Writer
	Stderr            io.Writer
}

type CurrentStatus struct {
	Live    *codex.Snapshot
	Managed *store.Record
}

type RunOptions struct {
	HomeDir    string
	WorkingDir string
	CopyConfig bool
	Args       []string
}

type AccountStatus struct {
	Record    store.Record
	Current   bool
	Quota     *codex.QuotaStatus
	Error     string
	Refreshed bool
}

type commandIO struct {
	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

var lookupCommand = exec.LookPath

var runCommand = func(name string, args []string, options commandIO) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = options.Dir
	if len(options.Env) > 0 {
		cmd.Env = options.Env
	}
	cmd.Stdin = options.Stdin
	cmd.Stdout = options.Stdout
	cmd.Stderr = options.Stderr
	return cmd.Run()
}

func New() (*App, error) {
	st, err := store.New()
	if err != nil {
		return nil, err
	}
	defaultHome, err := codex.ResolveDefaultHome()
	if err != nil {
		return nil, err
	}
	resolved, err := st.ResolveActivePath(defaultHome)
	if err != nil {
		return nil, err
	}
	return &App{
		Store:             st,
		ActiveHome:        resolved.Home,
		ActiveAuthPath:    resolved.AuthPath,
		ActiveConfigPath:  resolved.ConfigPath,
		ActivePathSource:  resolved.Source,
		ActivePathProfile: resolved.Profile,
		Stdin:             os.Stdin,
		Stdout:            os.Stdout,
		Stderr:            os.Stderr,
	}, nil
}

func (a *App) RefreshActivePath() error {
	defaultHome, err := codex.ResolveDefaultHome()
	if err != nil {
		return err
	}
	resolved, err := a.Store.ResolveActivePath(defaultHome)
	if err != nil {
		return err
	}
	a.ActiveHome = resolved.Home
	a.ActiveAuthPath = resolved.AuthPath
	a.ActiveConfigPath = resolved.ConfigPath
	a.ActivePathSource = resolved.Source
	a.ActivePathProfile = resolved.Profile
	return nil
}

func (a *App) AddCurrent(name string, overwrite bool) (*store.Record, error) {
	raw, err := codex.ReadAuthFile(a.ActiveHome)
	if err != nil {
		return nil, err
	}
	return a.addFromRaw(name, raw, overwrite)
}

func (a *App) AddFromFile(name, authPath string, overwrite bool) (*store.Record, error) {
	raw, err := os.ReadFile(authPath)
	if err != nil {
		return nil, fmt.Errorf("读取 auth 文件失败 (%s): %w", authPath, err)
	}
	return a.addFromRaw(name, raw, overwrite)
}

func (a *App) addFromRaw(name string, raw []byte, overwrite bool) (*store.Record, error) {
	snapshot, err := codex.SnapshotFromRawAuth(raw)
	if err != nil {
		return nil, err
	}
	resolvedName, err := a.resolveRecordName(name, *snapshot)
	if err != nil {
		return nil, err
	}

	record := &store.Record{
		Name:     resolvedName,
		SavedAt:  time.Now().UTC(),
		Snapshot: *snapshot,
		RawAuth:  append([]byte(nil), raw...),
	}
	if err := a.Store.Save(*record, overwrite); err != nil {
		return nil, err
	}
	return a.Store.Load(resolvedName)
}

func (a *App) List() ([]store.Record, error) {
	return a.Store.List()
}

func (a *App) Status() ([]AccountStatus, error) {
	records, err := a.Store.List()
	if err != nil {
		return nil, err
	}

	current, err := a.Current()
	if err != nil {
		return nil, err
	}

	statuses := make([]AccountStatus, 0, len(records))
	for _, record := range records {
		item := AccountStatus{
			Record:  record,
			Current: current.Managed != nil && strings.EqualFold(current.Managed.Name, record.Name),
		}

		quota, updatedRaw, err := codex.QueryQuota(record.RawAuth, codex.QuotaQueryOptions{})
		if err != nil {
			item.Error = err.Error()
		} else {
			item.Quota = quota
			item.Refreshed = quota.Refreshed
			if strings.TrimSpace(quota.Plan) != "" {
				record.Snapshot.Plan = strings.TrimSpace(quota.Plan)
				item.Record = record
			}
		}

		if len(updatedRaw) > 0 {
			record.RawAuth = append([]byte(nil), updatedRaw...)
			if snapshot, snapshotErr := codex.SnapshotFromRawAuth(updatedRaw); snapshotErr == nil {
				record.Snapshot = *snapshot
			}
		}

		if len(updatedRaw) > 0 || (item.Quota != nil && strings.TrimSpace(item.Quota.Plan) != "") {
			if saveErr := a.Store.Save(record, true); saveErr != nil && item.Error == "" {
				item.Error = saveErr.Error()
			}
			item.Record = record
		}

		statuses = append(statuses, item)
	}
	return statuses, nil
}

func (a *App) Current() (*CurrentStatus, error) {
	raw, err := codex.ReadAuthFile(a.ActiveHome)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &CurrentStatus{}, nil
		}
		return nil, err
	}

	snapshot, err := codex.SnapshotFromRawAuth(raw)
	if err != nil {
		return nil, err
	}

	records, err := a.Store.List()
	if err != nil {
		return nil, err
	}

	var matched *store.Record
	for i := range records {
		record := records[i]
		if record.Snapshot.AuthHash == snapshot.AuthHash {
			matched = &record
			break
		}
	}
	if matched == nil {
		for i := range records {
			record := records[i]
			if record.Snapshot.IdentityKey != "" && record.Snapshot.IdentityKey == snapshot.IdentityKey {
				matched = &record
				break
			}
		}
	}

	return &CurrentStatus{
		Live:    snapshot,
		Managed: matched,
	}, nil
}

func (a *App) Switch(name string) (*store.Record, string, error) {
	record, err := a.Store.Load(name)
	if err != nil {
		return nil, "", err
	}

	var backupPath string
	currentRaw, err := codex.ReadAuthFile(a.ActiveHome)
	if err == nil && len(currentRaw) > 0 {
		if bytes.Equal(currentRaw, record.RawAuth) {
			record.LastSwitchedAt = time.Now().UTC()
			if err := a.Store.Save(*record, true); err != nil {
				return nil, "", err
			}
			return record, "", nil
		}

		currentSnapshot, snapshotErr := codex.SnapshotFromRawAuth(currentRaw)
		if snapshotErr == nil &&
			currentSnapshot.IdentityKey != "" &&
			currentSnapshot.IdentityKey == record.Snapshot.IdentityKey &&
			currentSnapshot.IsNewerThan(record.Snapshot) {
			record.RawAuth = append([]byte(nil), currentRaw...)
			record.Snapshot = *currentSnapshot
			record.LastSwitchedAt = time.Now().UTC()
			if err := a.Store.Save(*record, true); err != nil {
				return nil, "", err
			}
			return record, "", nil
		}

		backupPath, err = a.Store.CreateBackup(currentRaw)
		if err != nil {
			return nil, "", err
		}
	}

	targetPath := a.ActiveAuthPath
	if err := codex.WriteFileWithBackup(targetPath, record.RawAuth); err != nil {
		return nil, "", err
	}

	record.LastSwitchedAt = time.Now().UTC()
	if err := a.Store.Save(*record, true); err != nil {
		return nil, "", err
	}
	return record, backupPath, nil
}

func (a *App) Login(name string, overwrite bool) (*store.Record, error) {
	if err := a.ensureNameAvailable(name, overwrite); err != nil {
		return nil, err
	}

	commandName, err := lookupCommand("codex")
	if err != nil {
		return nil, fmt.Errorf("未找到 codex 命令，请先安装 Codex CLI")
	}

	currentRaw, err := codex.ReadAuthFile(a.ActiveHome)
	switch {
	case err == nil && len(currentRaw) > 0:
		if err := runCommand(commandName, []string{"logout"}, commandIO{
			Env:    append(os.Environ(), "CODEX_HOME="+a.ActiveHome),
			Stdin:  a.Stdin,
			Stdout: a.Stdout,
			Stderr: a.Stderr,
		}); err != nil {
			return nil, fmt.Errorf("执行 codex logout 失败: %w", err)
		}
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return nil, err
	}

	if a.Stdout != nil {
		if _, writeErr := fmt.Fprintln(a.Stdout, "正在打开浏览器进行 Codex 登录，请在浏览器中完成授权..."); writeErr != nil {
			return nil, writeErr
		}
	}

	if err := runCommand(commandName, []string{"login"}, commandIO{
		Env:    append(os.Environ(), "CODEX_HOME="+a.ActiveHome),
		Stdin:  a.Stdin,
		Stdout: a.Stdout,
		Stderr: a.Stderr,
	}); err != nil {
		return nil, fmt.Errorf("执行 codex login 失败: %w", err)
	}

	return a.AddCurrent(name, overwrite)
}

func (a *App) Remove(name string) error {
	return a.Store.Delete(name)
}

func (a *App) Rename(oldName, newName string) (*store.Record, error) {
	return a.Store.Rename(oldName, newName)
}

func (a *App) ExportAuth(name, targetPath string) (*store.Record, string, error) {
	record, err := a.Store.Load(name)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(targetPath) == "" {
		return nil, "", fmt.Errorf("导出路径不能为空")
	}
	targetPath = filepath.Clean(targetPath)
	if err := codex.WriteFileWithBackup(targetPath, record.RawAuth); err != nil {
		return nil, "", err
	}
	return record, targetPath, nil
}

func (a *App) resolveRecordName(name string, snapshot codex.Snapshot) (string, error) {
	name = strings.TrimSpace(name)
	if name != "" {
		return name, nil
	}
	return a.nextAvailableName(store.ShellName(snapshot.SuggestedName()))
}

func (a *App) nextAvailableName(base string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "codex-account"
	}

	records, err := a.Store.List()
	if err != nil {
		return "", err
	}

	used := make(map[string]struct{}, len(records))
	for _, record := range records {
		used[strings.ToLower(strings.TrimSpace(record.Name))] = struct{}{}
	}

	candidate := base
	for index := 2; ; index++ {
		if _, exists := used[strings.ToLower(candidate)]; !exists {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, index)
	}
}

func (a *App) ExportHome(name, dir string, copyConfig bool) (*store.Record, string, error) {
	record, err := a.Store.Load(name)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(dir) == "" {
		return nil, "", fmt.Errorf("导出目录不能为空")
	}

	targetHome := filepath.Clean(dir)
	targetAuth := filepath.Join(targetHome, "auth.json")
	if err := codex.WriteFileWithBackup(targetAuth, record.RawAuth); err != nil {
		return nil, "", err
	}
	if copyConfig {
		if err := codex.CopyConfigFile(a.ActiveHome, targetHome); err != nil {
			return nil, "", err
		}
	}
	return record, targetHome, nil
}

func (a *App) Run(name string, opts RunOptions) (string, error) {
	record, err := a.Store.Load(name)
	if err != nil {
		return "", err
	}

	targetHome := opts.HomeDir
	if strings.TrimSpace(targetHome) == "" {
		targetHome, err = a.Store.DefaultHomeDir(record.Name)
		if err != nil {
			return "", err
		}
	}

	if _, _, err := a.ExportHome(record.Name, targetHome, opts.CopyConfig); err != nil {
		return "", err
	}

	commandName, err := lookupCommand("codex")
	if err != nil {
		return "", fmt.Errorf("未找到 codex 命令，请先安装 Codex CLI")
	}

	if err := runCommand(commandName, opts.Args, commandIO{
		Dir:    strings.TrimSpace(opts.WorkingDir),
		Env:    append(os.Environ(), "CODEX_HOME="+targetHome),
		Stdin:  a.Stdin,
		Stdout: a.Stdout,
		Stderr: a.Stderr,
	}); err != nil {
		return "", err
	}
	return targetHome, nil
}

func (a *App) ensureNameAvailable(name string, overwrite bool) error {
	name = strings.TrimSpace(name)
	if overwrite || name == "" {
		return nil
	}

	records, err := a.Store.List()
	if err != nil {
		return err
	}
	for _, record := range records {
		if strings.EqualFold(record.Name, name) {
			return fmt.Errorf("账号 %q 已存在，可使用 --force 覆盖", name)
		}
	}
	return nil
}
