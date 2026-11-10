// Package config loads and validates the asokit configuration file.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Default values applied when the config omits them.
const (
	DefaultCountry     = "us"
	DefaultSearchLimit = 200 // iTunes Search API hard maximum.
	DefaultStoreTop    = 50  // how many top results to persist per snapshot.
)

// Market is one storefront to track: a country plus its own keyword list. The
// same app is often marketed with different keywords per country/language.
type Market struct {
	// Country is the two-letter App Store storefront code, e.g. "us", "tr".
	Country string `yaml:"country" json:"country"`
	// Keywords is the list of search terms to track in this storefront.
	Keywords []string `yaml:"keywords" json:"keywords"`
}

// Relevance defines what makes a search result "the same type" as the tracked
// app, feeding the relevance metric. Both fields are optional; when GenreIDs is
// empty it defaults to the app's own App Store category (resolved at run time).
type Relevance struct {
	// GenreIDs are numeric App Store category ids (as strings, as the API
	// returns them), e.g. ["6015"] for Finance.
	GenreIDs []string `yaml:"genre_ids,omitempty" json:"genre_ids,omitempty"`
	// TitleTerms are words whose presence in a result's title also makes it
	// count as the same type.
	TitleTerms []string `yaml:"title_terms,omitempty" json:"title_terms,omitempty"`
}

// Competitor is another app tracked alongside ours; its position per keyword is
// derived from the stored results, so it is only visible down to store_top.
type Competitor struct {
	ID   int64  `yaml:"id" json:"id"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
}

// Config describes which app and markets to track.
type Config struct {
	// AppID is the numeric App Store identifier, shared across markets.
	AppID int64 `yaml:"app_id" json:"app_id"`
	// SearchLimit caps how many results to request per term (max 200).
	SearchLimit int `yaml:"search_limit" json:"search_limit"`
	// StoreTop is how many of the top results to persist per snapshot (for
	// competition metrics and competitor tracking). Clamped to SearchLimit.
	StoreTop int `yaml:"store_top" json:"store_top"`
	// Relevance configures the relevance metric. Optional.
	Relevance Relevance `yaml:"relevance,omitempty" json:"relevance"`
	// Competitors are other apps to track alongside ours. Optional.
	Competitors []Competitor `yaml:"competitors,omitempty" json:"competitors,omitempty"`
	// Markets is the list of storefronts to track.
	Markets []Market `yaml:"markets" json:"markets"`

	// Deprecated: legacy single-market fields, still accepted when loading an
	// old config and folded into Markets by Normalize. Not emitted on Save.
	Country  string   `yaml:"country,omitempty" json:"-"`
	Keywords []string `yaml:"keywords,omitempty" json:"-"`
}

// Load reads and validates a YAML config file from path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %q: %w", path, err)
	}

	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing config %q: %w", path, err)
	}

	c.Normalize()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Save writes the config to path as YAML. It is used by `asokit serve` when the
// config is edited in the browser. Note: this rewrites the file and does not
// preserve hand-written comments — a header line records that.
func (c *Config) Save(path string) error {
	c.Normalize()
	body, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	const header = "# asokit configuration — managed by `asokit serve`.\n" +
		"# Edits made in the browser rewrite this file; hand-written comments are not kept.\n"
	if err := os.WriteFile(path, append([]byte(header), body...), 0o600); err != nil {
		return fmt.Errorf("writing config %q: %w", path, err)
	}
	return nil
}

// Normalize applies defaults, folds legacy single-market fields into Markets,
// splits/dedups keywords, and dedups markets by country. Safe to call repeatedly.
func (c *Config) Normalize() {
	if c.SearchLimit <= 0 {
		c.SearchLimit = DefaultSearchLimit
	}
	if c.SearchLimit > DefaultSearchLimit {
		c.SearchLimit = DefaultSearchLimit
	}

	// store_top defaults to 50 and can never exceed how many results we fetch.
	if c.StoreTop <= 0 {
		c.StoreTop = DefaultStoreTop
	}
	if c.StoreTop > c.SearchLimit {
		c.StoreTop = c.SearchLimit
	}

	// Fold a legacy flat config (top-level country + keywords) into one market.
	if len(c.Markets) == 0 && (c.Country != "" || len(c.Keywords) > 0) {
		c.Markets = []Market{{Country: c.Country, Keywords: c.Keywords}}
	}
	c.Country = ""
	c.Keywords = nil

	// Normalize each market (lowercase country, split/dedup keywords). Duplicate
	// storefronts are left in place for Validate to report, rather than silently
	// dropped, so the user is told instead of losing keywords.
	markets := make([]Market, 0, len(c.Markets))
	for _, m := range c.Markets {
		markets = append(markets, Market{
			Country:  strings.ToLower(strings.TrimSpace(m.Country)),
			Keywords: SplitKeywords(m.Keywords),
		})
	}
	c.Markets = markets
}

// SplitKeywords splits each entry on commas/semicolons/newlines (so a list
// pasted from App Store Connect becomes individual terms), trims, drops empties,
// and de-duplicates case-insensitively while preserving order. CJK fullwidth
// and ideographic separators are handled too, since Chinese/Japanese lists
// commonly use them instead of the ASCII comma.
func SplitKeywords(in []string) []string {
	sep := func(r rune) bool {
		switch r {
		case ',', ';', '\n', '\r', '\t',
			'，', // ，fullwidth comma
			'、', // 、ideographic comma
			'；', // ；fullwidth semicolon
			'､': // ､halfwidth ideographic comma
			return true
		}
		return false
	}
	seen := make(map[string]bool)
	var out []string
	for _, raw := range in {
		for _, part := range strings.FieldsFunc(raw, sep) {
			k := strings.TrimSpace(part)
			if k == "" {
				continue
			}
			key := strings.ToLower(k)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, k)
		}
	}
	return out
}

// Validate checks that the config is usable.
func (c *Config) Validate() error {
	if c.AppID <= 0 {
		return fmt.Errorf("config: app_id must be a positive number")
	}
	if len(c.Markets) == 0 {
		return fmt.Errorf("config: at least one market is required")
	}
	seen := make(map[string]bool)
	for _, m := range c.Markets {
		if err := validateCountry(m.Country); err != nil {
			return err
		}
		if len(m.Keywords) == 0 {
			return fmt.Errorf("config: market %q needs at least one keyword", m.Country)
		}
		if seen[m.Country] {
			return fmt.Errorf("config: duplicate market %q", m.Country)
		}
		seen[m.Country] = true
	}
	for _, comp := range c.Competitors {
		if comp.ID <= 0 {
			return fmt.Errorf("config: competitor %q must have a positive id", comp.Name)
		}
		if comp.ID == c.AppID {
			return fmt.Errorf("config: competitor id %d is the tracked app itself", comp.ID)
		}
	}
	return nil
}

// validateCountry rejects anything that is not a real App Store storefront
// (ISO 3166-1 alpha-2) code. A frequent mistake is using a *language* code
// (zh, ja, ko, …) instead of the *country* code, so those get a suggestion.
func validateCountry(country string) error {
	if len(country) != 2 {
		return fmt.Errorf("config: country must be a two-letter code, got %q", country)
	}
	if iso3166Alpha2[country] {
		return nil
	}
	if suggestion, ok := languageToCountry[country]; ok {
		return fmt.Errorf("config: %q is a language code, not an App Store storefront — use %q for that store",
			country, suggestion)
	}
	return fmt.Errorf("config: %q is not a valid App Store storefront code (use a two-letter country code like us, de, tr)", country)
}

// languageToCountry maps common language codes people type by mistake to the
// storefront they most likely meant.
var languageToCountry = map[string]string{
	"zh": "cn", "ja": "jp", "ko": "kr", "uk": "gb", "en": "us",
	"el": "gr", "sv": "se", "da": "dk", "he": "il", "cs": "cz",
	"nb": "no", "nn": "no", "hi": "in", "fa": "ir", "vi": "vn",
}

// iso3166Alpha2 is the set of valid ISO 3166-1 alpha-2 country codes (lower
// case). Every App Store storefront is one of these, so it is a superset that
// never rejects a real storefront while still catching language codes and typos.
var iso3166Alpha2 = func() map[string]bool {
	const codes = "ad ae af ag ai al am ao aq ar as at au aw ax az ba bb bd be bf bg bh bi bj bl bm bn bo bq br bs bt bv bw by bz ca cc cd cf cg ch ci ck cl cm cn co cr cu cv cw cx cy cz de dj dk dm do dz ec ee eg eh er es et fi fj fk fm fo fr ga gb gd ge gf gg gh gi gl gm gn gp gq gr gs gt gu gw gy hk hm hn hr ht hu id ie il im in io iq ir is it je jm jo jp ke kg kh ki km kn kp kr kw ky kz la lb lc li lk lr ls lt lu lv ly ma mc md me mf mg mh mk ml mm mn mo mp mq mr ms mt mu mv mw mx my mz na nc ne nf ng ni nl no np nr nu nz om pa pe pf pg ph pk pl pm pn pr ps pt pw py qa re ro rs ru rw sa sb sc sd se sg sh si sj sk sl sm sn so sr ss st sv sx sy sz tc td tf tg th tj tk tl tm tn to tr tt tv tw tz ua ug um us uy uz va vc ve vg vi vn vu wf ws ye yt za zm zw"
	set := make(map[string]bool)
	for _, c := range strings.Fields(codes) {
		set[c] = true
	}
	return set
}()

// Market returns the market for the given country code, if present.
func (c *Config) Market(country string) (Market, bool) {
	country = strings.ToLower(strings.TrimSpace(country))
	for _, m := range c.Markets {
		if m.Country == country {
			return m, true
		}
	}
	return Market{}, false
}

// Countries returns the configured storefront codes, in order.
func (c *Config) Countries() []string {
	out := make([]string, 0, len(c.Markets))
	for _, m := range c.Markets {
		out = append(out, m.Country)
	}
	return out
}
