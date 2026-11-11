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

// firstApp returns cfg.Apps[0], failing the test if there is no app.
func firstApp(t *testing.T, cfg *Config) App {
	t.Helper()
	if len(cfg.Apps) == 0 {
		t.Fatalf("config has no apps: %+v", cfg)
	}
	return cfg.Apps[0]
}

// A legacy flat config (country + keywords at the top level) must migrate to a
// single app with one market without losing anything.
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
	app := firstApp(t, cfg)
	if app.ID != 571800810 {
		t.Errorf("app ID = %d", app.ID)
	}
	if len(app.Markets) != 1 {
		t.Fatalf("want 1 migrated market, got %v", app.Markets)
	}
	m := app.Markets[0]
	if m.Country != "tr" {
		t.Errorf("Country = %q, want tr (lowercased)", m.Country)
	}
	if len(m.Keywords) != 2 || m.Keywords[1] != "sleep tracker" {
		t.Errorf("Keywords = %v, want trimmed, empties dropped", m.Keywords)
	}
	// Legacy top-level fields must be cleared after migration.
	if cfg.AppID != 0 || cfg.Country != "" || cfg.Keywords != nil || cfg.Markets != nil {
		t.Errorf("legacy fields not cleared: %+v", cfg)
	}
}

// A legacy single-app config's top-level competitors and relevance must migrate
// onto the single app, not be lost.
func TestLoadLegacyMigratesCompetitorsAndRelevance(t *testing.T) {
	cfg, err := Load(writeTemp(t, `
app_id: 7
relevance:
  title_terms: [split, share]
competitors:
  - {id: 42, name: Rival}
markets:
  - {country: us, keywords: [x]}
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	app := firstApp(t, cfg)
	if len(app.Competitors) != 1 || app.Competitors[0].ID != 42 {
		t.Errorf("competitors not migrated to app: %+v", app.Competitors)
	}
	if len(app.Relevance.TitleTerms) != 2 {
		t.Errorf("relevance not migrated to app: %+v", app.Relevance)
	}
	if cfg.Competitors != nil || len(cfg.Relevance.TitleTerms) != 0 {
		t.Errorf("legacy top-level fields not cleared: %+v", cfg)
	}
}

// The native multi-app shape loads with per-app markets, competitors and
// relevance kept separate.
func TestLoadMultiApp(t *testing.T) {
	cfg, err := Load(writeTemp(t, `
search_limit: 200
apps:
  - id: 111
    name: Alpha
    competitors:
      - {id: 10, name: A-Rival}
    markets:
      - {country: us, keywords: [one, two]}
      - {country: de, keywords: [eins]}
  - id: 222
    relevance:
      title_terms: [beta]
    markets:
      - {country: tr, keywords: [uc]}
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Apps) != 2 {
		t.Fatalf("want 2 apps, got %d", len(cfg.Apps))
	}
	a, ok := cfg.App(111)
	if !ok || a.Name != "Alpha" || len(a.Markets) != 2 || len(a.Competitors) != 1 {
		t.Errorf("app 111 = %+v", a)
	}
	if got := a.Countries(); len(got) != 2 || got[0] != "us" || got[1] != "de" {
		t.Errorf("app 111 countries = %v", got)
	}
	b, ok := cfg.App(222)
	if !ok || len(b.Relevance.TitleTerms) != 1 || b.Relevance.TitleTerms[0] != "beta" {
		t.Errorf("app 222 = %+v", b)
	}
	if _, ok := b.Market("tr"); !ok {
		t.Errorf("app 222 should have a tr market")
	}
}

// Round-trip: loading a legacy file then Save must emit the apps shape and drop
// the old top-level keys.
func TestSaveEmitsAppsShape(t *testing.T) {
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
	if !strings.Contains(s, "apps:") || !strings.Contains(s, "markets:") {
		t.Errorf("saved file missing apps/markets:\n%s", s)
	}
	// The old top-level keys must be gone.
	for _, bad := range []string{"\ncountry:", "\nkeywords:", "\napp_id:"} {
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
	app := firstApp(t, cfg)
	if len(app.Markets) != 2 {
		t.Fatalf("want 2 markets, got %d", len(app.Markets))
	}
	us, ok := app.Market("US") // case-insensitive lookup
	if !ok || len(us.Keywords) != 3 || us.Keywords[0] != "sleep tracker" {
		t.Errorf("us market = %+v (comma entry should split)", us)
	}
	if got := app.Countries(); len(got) != 2 || got[0] != "us" || got[1] != "de" {
		t.Errorf("Countries = %v, want [us de] in order", got)
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := map[string]string{
		"missing app id":      "markets:\n  - {country: us, keywords: [x]}\n",
		"no markets":          "app_id: 1\n",
		"bad country":         "app_id: 1\nmarkets:\n  - {country: usa, keywords: [x]}\n",
		"market no kw":        "app_id: 1\nmarkets:\n  - {country: us, keywords: []}\n",
		"duplicate market":    "app_id: 1\nmarkets:\n  - {country: us, keywords: [x]}\n  - {country: us, keywords: [y]}\n",
		"duplicate app":       "apps:\n  - {id: 1, markets: [{country: us, keywords: [x]}]}\n  - {id: 1, markets: [{country: de, keywords: [y]}]}\n",
		"app without markets": "apps:\n  - {id: 5}\n",
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
	got := firstApp(t, cfg).Markets[0].Keywords
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
	app := firstApp(t, cfg)
	if len(app.Competitors) != 0 || len(app.Relevance.GenreIDs) != 0 || len(app.Relevance.TitleTerms) != 0 {
		t.Errorf("expected empty new fields, got %+v", app)
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

// Save then Load must round-trip the per-app fields, so a browser "Save" or a
// hand edit never drops an app's competitors / relevance or the global store_top.
func TestSaveLoadRoundTripsNewFields(t *testing.T) {
	path := writeTemp(t, "app_id: 1\nmarkets:\n  - {country: us, keywords: [x]}\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.StoreTop = 25
	cfg.Apps[0].Relevance = Relevance{GenreIDs: []string{"6015"}, TitleTerms: []string{"budget"}}
	cfg.Apps[0].Competitors = []Competitor{{ID: 123, Name: "Rival"}}
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
	app := firstApp(t, got)
	if len(app.Relevance.GenreIDs) != 1 || app.Relevance.GenreIDs[0] != "6015" ||
		len(app.Relevance.TitleTerms) != 1 || app.Relevance.TitleTerms[0] != "budget" {
		t.Errorf("relevance not round-tripped: %+v", app.Relevance)
	}
	if len(app.Competitors) != 1 || app.Competitors[0].ID != 123 || app.Competitors[0].Name != "Rival" {
		t.Errorf("competitors not round-tripped: %+v", app.Competitors)
	}
}

// Phrases are an optional second group: split/deduped like keywords, round-trip
// through Save, and a market with only phrases (no keywords) is valid. Terms()
// unions both groups without duplicates.
func TestPhrases(t *testing.T) {
	path := writeTemp(t, "app_id: 1\nmarkets:\n  - country: us\n    keywords: [sleep, calm]\n    phrases: [\"sleep sounds, sleep sounds\", \"white noise\"]\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	m := firstApp(t, cfg).Markets[0]
	if len(m.Phrases) != 2 || m.Phrases[0] != "sleep sounds" || m.Phrases[1] != "white noise" {
		t.Fatalf("phrases = %v, want [sleep sounds, white noise] (split+dedup)", m.Phrases)
	}
	if terms := m.Terms(); len(terms) != 4 || terms[0] != "sleep" || terms[2] != "sleep sounds" {
		t.Errorf("Terms() = %v, want keywords then phrases", terms)
	}

	// Phrases-only market is valid; Save round-trips phrases.
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil || len(firstApp(t, got).Markets[0].Phrases) != 2 {
		t.Fatalf("phrases not round-tripped: %+v (err %v)", got.Apps, err)
	}

	if _, err := Load(writeTemp(t, "app_id: 1\nmarkets:\n  - country: de\n    phrases: [schlaf gut]\n")); err != nil {
		t.Errorf("phrases-only market should be valid, got %v", err)
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
