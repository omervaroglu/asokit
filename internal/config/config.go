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
)

// Market is one storefront to track: a country plus its own keyword list. The
// same app is often marketed with different keywords per country/language.
type Market struct {
	// Country is the two-letter App Store storefront code, e.g. "us", "tr".
	Country string `yaml:"country" json:"country"`
	// Keywords is the list of search terms to track in this storefront.
	Keywords []string `yaml:"keywords" json:"keywords"`
}

// Config describes which app and markets to track.
type Config struct {
	// AppID is the numeric App Store identifier, shared across markets.
	AppID int64 `yaml:"app_id" json:"app_id"`
	// SearchLimit caps how many results to request per term (max 200).
	SearchLimit int `yaml:"search_limit" json:"search_limit"`
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
// and de-duplicates case-insensitively while preserving order.
func SplitKeywords(in []string) []string {
	sep := func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\r' }
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
		if len(m.Country) != 2 {
			return fmt.Errorf("config: country must be a two-letter code, got %q", m.Country)
		}
		if len(m.Keywords) == 0 {
			return fmt.Errorf("config: market %q needs at least one keyword", m.Country)
		}
		if seen[m.Country] {
			return fmt.Errorf("config: duplicate market %q", m.Country)
		}
		seen[m.Country] = true
	}
	return nil
}

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
