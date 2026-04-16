package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ResolveDefaultHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("获取用户目录失败: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}

func ResolveActiveHome() (string, error) {
	if env := strings.TrimSpace(os.Getenv("CODEX_HOME")); env != "" {
		return filepath.Clean(env), nil
	}
	return ResolveDefaultHome()
}

func AuthFilePath(home string) string {
	return filepath.Join(home, "auth.json")
}

func ConfigFilePath(home string) string {
	return filepath.Join(home, "config.toml")
}

func ReadAuthFile(home string) ([]byte, error) {
	path := AuthFilePath(home)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 auth.json 失败 (%s): %w", path, err)
	}
	return raw, nil
}

func CopyConfigFile(srcHome, dstHome string) error {
	src := ConfigFilePath(srcHome)
	raw, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取 config.toml 失败 (%s): %w", src, err)
	}
	dst := ConfigFilePath(dstHome)
	return WriteFileWithBackup(dst, raw)
}

func WriteFileWithBackup(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建目录失败 (%s): %w", filepath.Dir(path), err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		return fmt.Errorf("写入临时文件失败 (%s): %w", tmp, err)
	}

	backup := path + ".bak"
	_ = os.Remove(backup)

	if _, err := os.Stat(path); err == nil {
		if err := os.Rename(path, backup); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("创建备份失败 (%s): %w", backup, err)
		}
	}

	if err := os.Rename(tmp, path); err != nil {
		if _, restoreErr := os.Stat(backup); restoreErr == nil {
			_ = os.Rename(backup, path)
		}
		_ = os.Remove(tmp)
		return fmt.Errorf("替换文件失败 (%s): %w", path, err)
	}

	_ = os.Remove(backup)
	return nil
}
