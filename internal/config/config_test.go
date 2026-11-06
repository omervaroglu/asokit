package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "asokit.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	path := writeTemp(t, `
app_id: 571800810
country: TR
keywords:
  - meditation
  - "  sleep tracker  "
  - ""
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AppID != 571800810 {
		t.Errorf("AppID = %d", cfg.AppID)
	}
	if cfg.Country != "tr" {
		t.Errorf("Country = %q, want tr (lowercased)", cfg.Country)
	}
	if len(cfg.Keywords) != 2 {
		t.Fatalf("Keywords = %v, want 2 (empties dropped)", cfg.Keywords)
	}
	if cfg.Keywords[1] != "sleep tracker" {
		t.Errorf("Keyword not trimmed: %q", cfg.Keywords[1])
	}
	if cfg.SearchLimit != DefaultSearchLimit {
		t.Errorf("SearchLimit = %d, want default %d", cfg.SearchLimit, DefaultSearchLimit)
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := map[string]string{
		"missing app_id": "keywords:\n  - x\n",
		"no keywords":    "app_id: 1\n",
		"bad country":    "app_id: 1\ncountry: usa\nkeywords:\n  - x\n",
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeTemp(t, content)); err == nil {
				t.Errorf("expected error for %s", name)
			}
		})
	}
}

func TestKeywordSplittingAndDedup(t *testing.T) {
	// A comma/semicolon list in one entry, plus a duplicate and casing.
	path := writeTemp(t, "app_id: 1\nkeywords:\n  - \"hesap, bölme ;ortak,masraf\"\n  - \"Masraf\"\n  - \"kira\"\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"hesap", "bölme", "ortak", "masraf", "kira"}
	if len(cfg.Keywords) != len(want) {
		t.Fatalf("keywords = %v, want %v", cfg.Keywords, want)
	}
	for i, w := range want {
		if cfg.Keywords[i] != w {
			t.Errorf("keyword[%d] = %q, want %q (order + dedup preserved)", i, cfg.Keywords[i], w)
		}
	}
}

func TestSearchLimitClamped(t *testing.T) {
	cfg, err := Load(writeTemp(t, "app_id: 1\nsearch_limit: 5000\nkeywords:\n  - x\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SearchLimit != DefaultSearchLimit {
		t.Errorf("SearchLimit = %d, want clamped to %d", cfg.SearchLimit, DefaultSearchLimit)
	}
}
