package store

import (
	"path/filepath"
	"testing"
)

func TestPathProfilesResolveActivePath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_SWITCH_HOME", root)
	t.Setenv("CODEX_HOME", "")

	st, err := New()
	if err != nil {
		t.Fatal(err)
	}

	profile, err := st.SavePathProfile("work", filepath.Join(root, "profiles", "work", "auth.json"), true)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Home != filepath.Join(root, "profiles", "work") {
		t.Fatalf("expected auth file path to normalize to home dir, got %q", profile.Home)
	}

	resolved, err := st.ResolveActivePath(filepath.Join(root, "default-home"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source != "profile" {
		t.Fatalf("expected profile source, got %q", resolved.Source)
	}
	if resolved.Profile != "work" {
		t.Fatalf("expected active profile work, got %q", resolved.Profile)
	}
	if resolved.AuthPath != filepath.Join(profile.Home, "auth.json") {
		t.Fatalf("unexpected auth path: %q", resolved.AuthPath)
	}
}

func TestPathProfilesRespectCODEXHOMEOverride(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_SWITCH_HOME", root)

	st, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SavePathProfile("work", filepath.Join(root, "profiles", "work"), true); err != nil {
		t.Fatal(err)
	}

	overrideHome := filepath.Join(root, "env-home")
	t.Setenv("CODEX_HOME", overrideHome)

	resolved, err := st.ResolveActivePath(filepath.Join(root, "default-home"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source != "env" {
		t.Fatalf("expected env source, got %q", resolved.Source)
	}
	if resolved.Home != overrideHome {
		t.Fatalf("expected env home %q, got %q", overrideHome, resolved.Home)
	}
	if resolved.Profile != "" {
		t.Fatalf("expected env override to suppress active profile, got %q", resolved.Profile)
	}
}
