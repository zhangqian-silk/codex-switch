package store

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"codex-switch/internal/codex"
)

type Store struct {
	root string
}

type Record struct {
	Name           string          `json:"name"`
	SavedAt        time.Time       `json:"saved_at"`
	LastSwitchedAt time.Time       `json:"last_switched_at,omitempty"`
	Snapshot       codex.Snapshot  `json:"snapshot"`
	RawAuth        json.RawMessage `json:"raw_auth"`
}

func New() (*Store, error) {
	candidates, err := resolveRootCandidates()
	if err != nil {
		return nil, err
	}

	var createErrs []string
	for _, root := range candidates {
		s := &Store{root: root}
		failed := false
		for _, dir := range []string{s.AccountsDir(), s.HomesDir(), s.BackupsDir()} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				createErrs = append(createErrs, fmt.Sprintf("%s: %v", dir, err))
				failed = true
				break
			}
		}
		if !failed {
			return s, nil
		}
	}

	return nil, fmt.Errorf("创建存储目录失败: %s", strings.Join(createErrs, "; "))
}

func (s *Store) Root() string {
	return s.root
}

func (s *Store) AccountsDir() string {
	return filepath.Join(s.root, "accounts")
}

func (s *Store) HomesDir() string {
	return filepath.Join(s.root, "homes")
}

func (s *Store) BackupsDir() string {
	return filepath.Join(s.root, "backups")
}

func (s *Store) Save(record Record, overwrite bool) error {
	name, err := NormalizeName(record.Name)
	if err != nil {
		return err
	}
	record.Name = name

	path := s.recordPath(record.Name)
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("账号 %q 已存在，可使用 --force 覆盖", record.Name)
		}
	}

	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化账号失败: %w", err)
	}
	if err := codex.WriteFileWithBackup(path, raw); err != nil {
		return err
	}
	return nil
}

func (s *Store) Load(name string) (*Record, error) {
	name, err := NormalizeName(name)
	if err != nil {
		return nil, err
	}

	record, err := s.resolveRecord(name)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Store) Delete(name string) error {
	record, err := s.Load(name)
	if err != nil {
		return err
	}
	path := s.recordPath(record.Name)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("账号 %q 不存在", record.Name)
		}
		return fmt.Errorf("删除账号失败: %w", err)
	}
	return nil
}

func (s *Store) Rename(oldName, newName string) (*Record, error) {
	oldName, err := NormalizeName(oldName)
	if err != nil {
		return nil, err
	}
	newName, err = NormalizeName(newName)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(oldName, newName) {
		record, err := s.Load(oldName)
		if err != nil {
			return nil, err
		}
		record.Name = newName
		if err := s.Save(*record, true); err != nil {
			return nil, err
		}
		return s.Load(newName)
	}

	record, err := s.Load(oldName)
	if err != nil {
		return nil, err
	}
	record.Name = newName
	if err := s.Save(*record, false); err != nil {
		return nil, err
	}
	if err := s.Delete(oldName); err != nil {
		return nil, err
	}
	return s.Load(newName)
}

func (s *Store) List() ([]Record, error) {
	entries, err := os.ReadDir(s.AccountsDir())
	if err != nil {
		return nil, fmt.Errorf("读取账号目录失败: %w", err)
	}

	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.AccountsDir(), entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("读取账号文件失败 (%s): %w", entry.Name(), err)
		}
		var record Record
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, fmt.Errorf("解析账号文件失败 (%s): %w", entry.Name(), err)
		}
		records = append(records, record)
	}

	sort.Slice(records, func(i, j int) bool {
		return strings.ToLower(records[i].Name) < strings.ToLower(records[j].Name)
	})
	return records, nil
}

func (s *Store) DefaultHomeDir(name string) (string, error) {
	name, err := NormalizeName(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.HomesDir(), slugForName(name)), nil
}

func (s *Store) CreateBackup(raw []byte) (string, error) {
	name := "auth-" + time.Now().Format("20060102-150405.000") + ".json"
	path := filepath.Join(s.BackupsDir(), name)
	if err := codex.WriteFileWithBackup(path, raw); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) recordPath(name string) string {
	return filepath.Join(s.AccountsDir(), slugForName(name)+".json")
}

func NormalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("账号名称不能为空")
	}
	return name, nil
}

func ShellName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}

	var builder strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(unicode.ToLower(r))
			lastDash = false
		case builder.Len() > 0 && !lastDash:
			builder.WriteByte('-')
			lastDash = true
		}
	}

	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "codex-account"
	}
	return result
}

func slugForName(name string) string {
	sum := sha1.Sum([]byte(strings.ToLower(strings.TrimSpace(name))))
	return hex.EncodeToString(sum[:])
}

func (s *Store) resolveRecord(input string) (Record, error) {
	records, err := s.List()
	if err != nil {
		return Record{}, err
	}

	input = strings.TrimSpace(input)
	inputFolded := strings.ToLower(input)
	inputShell := ShellName(input)
	for _, record := range records {
		if strings.EqualFold(strings.TrimSpace(record.Name), input) {
			return record, nil
		}
	}

	if inputShell != "" {
		shellMatches := make([]Record, 0, 1)
		for _, record := range records {
			if inputShell == ShellName(record.Name) {
				shellMatches = append(shellMatches, record)
			}
		}
		if len(shellMatches) == 1 {
			return shellMatches[0], nil
		}
		if len(shellMatches) > 1 {
			return Record{}, ambiguousRecordError(input, shellMatches)
		}
	}

	matches := make([]Record, 0, 1)
	for _, record := range records {
		for _, candidate := range recordLookupKeys(record) {
			if candidate == "" {
				continue
			}
			if inputFolded == strings.ToLower(candidate) || (inputShell != "" && inputShell == ShellName(candidate)) {
				matches = append(matches, record)
				break
			}
		}
	}

	switch len(matches) {
	case 0:
		return Record{}, fmt.Errorf("账号 %q 不存在", input)
	case 1:
		return matches[0], nil
	default:
		return Record{}, ambiguousRecordError(input, matches)
	}
}

func ambiguousRecordError(input string, matches []Record) error {
	names := make([]string, 0, len(matches))
	for _, record := range matches {
		names = append(names, record.Name)
	}
	sort.Strings(names)
	return fmt.Errorf("账号 %q 匹配到多个结果: %s", input, strings.Join(names, ", "))
}

func recordLookupKeys(record Record) []string {
	keys := []string{
		record.Name,
		record.Snapshot.AccountName,
		record.Snapshot.Email,
		record.Snapshot.AccountID,
		record.Snapshot.UserID,
	}
	if email := strings.TrimSpace(record.Snapshot.Email); email != "" {
		if at := strings.Index(email, "@"); at > 0 {
			keys = append(keys, email[:at])
		}
	}
	return keys
}

func resolveRootCandidates() ([]string, error) {
	if env := strings.TrimSpace(os.Getenv("CODEX_SWITCH_HOME")); env != "" {
		return []string{filepath.Clean(env)}, nil
	}
	candidates := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	appendUnique := func(path string) {
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		candidates = append(candidates, path)
	}

	if dir, err := os.UserConfigDir(); err == nil {
		appendUnique(filepath.Join(dir, "codex-switch"))
	}
	if env := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); env != "" {
		appendUnique(filepath.Join(env, "codex-switch"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		appendUnique(filepath.Join(home, ".codex-switch"))
		appendUnique(filepath.Join(home, ".codex", "switch-data"))
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("获取配置目录失败")
	}
	return candidates, nil
}
