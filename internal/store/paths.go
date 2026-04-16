package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codex-switch/internal/codex"
)

type PathProfile struct {
	Name string `json:"name"`
	Home string `json:"home"`
}

type ResolvedPath struct {
	Home       string
	AuthPath   string
	ConfigPath string
	Source     string
	Profile    string
}

type pathConfig struct {
	Current  string        `json:"current,omitempty"`
	Profiles []PathProfile `json:"profiles,omitempty"`
}

func (s *Store) ListPathProfiles() ([]PathProfile, string, error) {
	cfg, err := s.loadPathConfig()
	if err != nil {
		return nil, "", err
	}

	profiles := append([]PathProfile(nil), cfg.Profiles...)
	sort.Slice(profiles, func(i, j int) bool {
		return strings.ToLower(profiles[i].Name) < strings.ToLower(profiles[j].Name)
	})
	return profiles, cfg.Current, nil
}

func (s *Store) SavePathProfile(name, target string, use bool) (*PathProfile, error) {
	name, err := NormalizeName(name)
	if err != nil {
		return nil, err
	}
	home, err := normalizeManagedHomePath(target)
	if err != nil {
		return nil, err
	}

	cfg, err := s.loadPathConfig()
	if err != nil {
		return nil, err
	}

	profile := PathProfile{Name: name, Home: home}
	replaced := false
	for index := range cfg.Profiles {
		if strings.EqualFold(cfg.Profiles[index].Name, name) {
			cfg.Profiles[index] = profile
			replaced = true
			break
		}
	}
	if !replaced {
		cfg.Profiles = append(cfg.Profiles, profile)
	}
	if use {
		cfg.Current = name
	}

	if err := s.savePathConfig(cfg); err != nil {
		return nil, err
	}
	return &profile, nil
}

func (s *Store) UsePathProfile(name string) (*PathProfile, error) {
	name, err := NormalizeName(name)
	if err != nil {
		return nil, err
	}

	cfg, err := s.loadPathConfig()
	if err != nil {
		return nil, err
	}
	for _, profile := range cfg.Profiles {
		if strings.EqualFold(profile.Name, name) {
			cfg.Current = profile.Name
			if err := s.savePathConfig(cfg); err != nil {
				return nil, err
			}
			return &profile, nil
		}
	}
	return nil, fmt.Errorf("path profile %q 不存在", name)
}

func (s *Store) DeletePathProfile(name string) error {
	name, err := NormalizeName(name)
	if err != nil {
		return err
	}

	cfg, err := s.loadPathConfig()
	if err != nil {
		return err
	}

	kept := make([]PathProfile, 0, len(cfg.Profiles))
	deleted := false
	for _, profile := range cfg.Profiles {
		if strings.EqualFold(profile.Name, name) {
			deleted = true
			continue
		}
		kept = append(kept, profile)
	}
	if !deleted {
		return fmt.Errorf("path profile %q 不存在", name)
	}

	cfg.Profiles = kept
	if strings.EqualFold(cfg.Current, name) {
		cfg.Current = ""
	}
	return s.savePathConfig(cfg)
}

func (s *Store) ClearActivePathProfile() error {
	cfg, err := s.loadPathConfig()
	if err != nil {
		return err
	}
	cfg.Current = ""
	return s.savePathConfig(cfg)
}

func (s *Store) ResolveActivePath(defaultHome string) (*ResolvedPath, error) {
	if env := strings.TrimSpace(os.Getenv("CODEX_HOME")); env != "" {
		home := filepath.Clean(env)
		return &ResolvedPath{
			Home:       home,
			AuthPath:   codex.AuthFilePath(home),
			ConfigPath: codex.ConfigFilePath(home),
			Source:     "env",
		}, nil
	}

	cfg, err := s.loadPathConfig()
	if err != nil {
		return nil, err
	}
	if cfg.Current != "" {
		for _, profile := range cfg.Profiles {
			if strings.EqualFold(profile.Name, cfg.Current) {
				return &ResolvedPath{
					Home:       profile.Home,
					AuthPath:   codex.AuthFilePath(profile.Home),
					ConfigPath: codex.ConfigFilePath(profile.Home),
					Source:     "profile",
					Profile:    profile.Name,
				}, nil
			}
		}
	}

	home := filepath.Clean(defaultHome)
	return &ResolvedPath{
		Home:       home,
		AuthPath:   codex.AuthFilePath(home),
		ConfigPath: codex.ConfigFilePath(home),
		Source:     "default",
	}, nil
}

func (s *Store) PathsConfigPath() string {
	return filepath.Join(s.root, "paths.json")
}

func (s *Store) loadPathConfig() (*pathConfig, error) {
	raw, err := os.ReadFile(s.PathsConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &pathConfig{}, nil
		}
		return nil, fmt.Errorf("读取 path 配置失败: %w", err)
	}

	var cfg pathConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("解析 path 配置失败: %w", err)
	}
	return &cfg, nil
}

func (s *Store) savePathConfig(cfg *pathConfig) error {
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 path 配置失败: %w", err)
	}
	return codex.WriteFileWithBackup(s.PathsConfigPath(), raw)
}

func normalizeManagedHomePath(target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("path 不能为空")
	}

	cleaned := filepath.Clean(target)
	if strings.EqualFold(filepath.Base(cleaned), "auth.json") {
		return filepath.Dir(cleaned), nil
	}
	return cleaned, nil
}
