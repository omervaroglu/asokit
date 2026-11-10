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
app_id: 571800810
markets:
  - country: us
    keywords: ["sleep tracker, meditation", "calm"]
  - country: de
    keywords: [schlaf, entspannung]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Markets) != 2 {
		t.Fatalf("want 2 markets, got %d", len(cfg.Markets))
	}
	us, ok := cfg.Market("US") // case-insensitive lookup
	if !ok || len(us.Keywords) != 3 || us.Keywords[0] != "sleep tracker" {
		t.Errorf("us market = %+v (comma entry should split)", us)
	}
	if got := cfg.Countries(); len(got) != 2 || got[0] != "us" || got[1] != "de" {
		t.Errorf("Countries = %v, want [us de] in order", got)
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

func TestStorefrontValidation(t *testing.T) {
	// Language codes and typos are rejected; real storefronts pass.
	bad := map[string]string{
		"zh (Chinese language)":  "app_id: 1\nmarkets:\n  - {country: zh, keywords: [x]}\n",
		"ja (Japanese language)": "app_id: 1\nmarkets:\n  - {country: ja, keywords: [x]}\n",
		"xx (not a country)":     "app_id: 1\nmarkets:\n  - {country: xx, keywords: [x]}\n",
	}
	for name, content := range bad {
		t.Run("reject "+name, func(t *testing.T) {
			if _, err := Load(writeTemp(t, content)); err == nil {
				t.Errorf("expected error for %s", name)
			}
		})
	}

	// The China storefront (cn) is accepted.
	if _, err := Load(writeTemp(t, "app_id: 1\nmarkets:\n  - {country: cn, keywords: [x]}\n")); err != nil {
		t.Errorf("cn should be a valid storefront: %v", err)
	}

	// The zh error suggests cn.
	_, err := Load(writeTemp(t, "app_id: 1\nmarkets:\n  - {country: zh, keywords: [x]}\n"))
	if err == nil || !strings.Contains(err.Error(), "cn") {
		t.Errorf("zh error should suggest cn, got: %v", err)
	}
}

func TestKeywordSplittingAndDedup(t *testing.T) {
	path := writeTemp(t, "app_id: 1\nmarkets:\n  - country: us\n    keywords:\n      - \"sleep tracker, meditation ;calm,focus\"\n      - \"Focus\"\n      - \"relax\"\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"sleep tracker", "meditation", "calm", "focus", "relax"}
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

func TestSplitChinesePunctuation(t *testing.T) {
	// Fullwidth comma, ideographic comma and fullwidth semicolon all split.
	got := SplitKeywords([]string{"冥想，睡眠、放松；打坐"})
	want := []string{"冥想", "睡眠", "放松", "打坐"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
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

// A config written before the competition feature (no store_top/relevance/
// competitors) must still load, defaulting store_top and leaving the new fields
// empty — the back-compat guarantee in the acceptance criteria.
func TestBackCompatNoNewFields(t *testing.T) {
	cfg, err := Load(writeTemp(t, "app_id: 1\nmarkets:\n  - {country: us, keywords: [x]}\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.StoreTop != DefaultStoreTop {
		t.Errorf("StoreTop = %d, want default %d", cfg.StoreTop, DefaultStoreTop)
	}
	if len(cfg.Competitors) != 0 || len(cfg.Relevance.GenreIDs) != 0 || len(cfg.Relevance.TitleTerms) != 0 {
		t.Errorf("expected empty new fields, got %+v", cfg)
	}
}

func TestStoreTopClampedToSearchLimit(t *testing.T) {
	cfg, err := Load(writeTemp(t, "app_id: 1\nsearch_limit: 30\nstore_top: 100\nmarkets:\n  - {country: us, keywords: [x]}\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.StoreTop != 30 {
		t.Errorf("StoreTop = %d, want clamped to search_limit 30", cfg.StoreTop)
	}
}

// Save then Load must round-trip the new fields, so a browser "Save" or a hand
// edit never drops competitors / relevance / store_top.
func TestSaveLoadRoundTripsNewFields(t *testing.T) {
	path := writeTemp(t, "app_id: 1\nmarkets:\n  - {country: us, keywords: [x]}\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.StoreTop = 25
	cfg.Relevance = Relevance{GenreIDs: []string{"6015"}, TitleTerms: []string{"budget"}}
	cfg.Competitors = []Competitor{{ID: 123, Name: "Rival"}}
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.StoreTop != 25 {
		t.Errorf("StoreTop = %d, want 25", got.StoreTop)
	}
	if len(got.Relevance.GenreIDs) != 1 || got.Relevance.GenreIDs[0] != "6015" ||
		len(got.Relevance.TitleTerms) != 1 || got.Relevance.TitleTerms[0] != "budget" {
		t.Errorf("relevance not round-tripped: %+v", got.Relevance)
	}
	if len(got.Competitors) != 1 || got.Competitors[0].ID != 123 || got.Competitors[0].Name != "Rival" {
		t.Errorf("competitors not round-tripped: %+v", got.Competitors)
	}
}

func TestCompetitorValidation(t *testing.T) {
	// Competitor id must be positive and must not be the tracked app itself.
	if _, err := Load(writeTemp(t, "app_id: 1\ncompetitors:\n  - {id: 0, name: X}\nmarkets:\n  - {country: us, keywords: [x]}\n")); err == nil {
		t.Error("expected error for competitor id 0")
	}
	if _, err := Load(writeTemp(t, "app_id: 7\ncompetitors:\n  - {id: 7, name: Self}\nmarkets:\n  - {country: us, keywords: [x]}\n")); err == nil {
		t.Error("expected error for competitor equal to app_id")
	}
}
