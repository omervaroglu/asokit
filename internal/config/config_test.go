package config

import (
	"os"
	"path/filepath"
	"strings"
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

// A legacy flat config (country + keywords at the top level) must migrate to a
// single market without losing anything.
func TestLoadLegacyFlatMigrates(t *testing.T) {
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
	if len(cfg.Markets) != 1 {
		t.Fatalf("want 1 migrated market, got %v", cfg.Markets)
	}
	m := cfg.Markets[0]
	if m.Country != "tr" {
		t.Errorf("Country = %q, want tr (lowercased)", m.Country)
	}
	if len(m.Keywords) != 2 || m.Keywords[1] != "sleep tracker" {
		t.Errorf("Keywords = %v, want trimmed, empties dropped", m.Keywords)
	}
	if cfg.Country != "" || cfg.Keywords != nil {
		t.Errorf("legacy fields not cleared: country=%q keywords=%v", cfg.Country, cfg.Keywords)
	}
}

// Round-trip: loading a legacy file then Save must emit only the markets shape.
func TestSaveEmitsMarketsOnly(t *testing.T) {
	path := writeTemp(t, "app_id: 1\ncountry: tr\nkeywords:\n  - a\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, _ := os.ReadFile(path)
	s := string(data)
	if !strings.Contains(s, "markets:") {
		t.Errorf("saved file missing markets:\n%s", s)
	}
	// The old top-level "country:" / "keywords:" keys must be gone.
	for _, bad := range []string{"\ncountry:", "\nkeywords:"} {
		if strings.Contains(s, bad) {
			t.Errorf("saved file still has legacy key %q:\n%s", bad, s)
		}
	}
}

func TestLoadMultiMarket(t *testing.T) {
	path := writeTemp(t, `
app_id: 6741804095
markets:
  - country: tr
    keywords: ["hesap, bölme", "ortak"]
  - country: de
    keywords: [konto, teilen]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Markets) != 2 {
		t.Fatalf("want 2 markets, got %d", len(cfg.Markets))
	}
	tr, ok := cfg.Market("TR") // case-insensitive lookup
	if !ok || len(tr.Keywords) != 3 || tr.Keywords[0] != "hesap" {
		t.Errorf("tr market = %+v (comma entry should split)", tr)
	}
	if got := cfg.Countries(); len(got) != 2 || got[0] != "tr" || got[1] != "de" {
		t.Errorf("Countries = %v, want [tr de] in order", got)
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := map[string]string{
		"missing app_id":   "markets:\n  - {country: us, keywords: [x]}\n",
		"no markets":       "app_id: 1\n",
		"bad country":      "app_id: 1\nmarkets:\n  - {country: usa, keywords: [x]}\n",
		"market no kw":     "app_id: 1\nmarkets:\n  - {country: us, keywords: []}\n",
		"duplicate market": "app_id: 1\nmarkets:\n  - {country: us, keywords: [x]}\n  - {country: us, keywords: [y]}\n",
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
	path := writeTemp(t, "app_id: 1\nmarkets:\n  - country: tr\n    keywords:\n      - \"hesap, bölme ;ortak,masraf\"\n      - \"Masraf\"\n      - \"kira\"\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"hesap", "bölme", "ortak", "masraf", "kira"}
	got := cfg.Markets[0].Keywords
	if len(got) != len(want) {
		t.Fatalf("keywords = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("keyword[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestSearchLimitClamped(t *testing.T) {
	cfg, err := Load(writeTemp(t, "app_id: 1\nsearch_limit: 5000\nmarkets:\n  - {country: us, keywords: [x]}\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SearchLimit != DefaultSearchLimit {
		t.Errorf("SearchLimit = %d, want clamped to %d", cfg.SearchLimit, DefaultSearchLimit)
	}
}
