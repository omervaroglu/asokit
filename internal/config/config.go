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

// Config describes which app and keywords to track.
type Config struct {
	// AppID is the numeric App Store identifier of the app being tracked
	// (the "id" in an App Store URL, e.g. 571800810).
	AppID int64 `yaml:"app_id"`
	// Country is the two-letter App Store storefront code, e.g. "us", "tr".
	Country string `yaml:"country"`
	// Keywords is the list of search terms to track.
	Keywords []string `yaml:"keywords"`
	// SearchLimit caps how many results to request per term (max 200).
	SearchLimit int `yaml:"search_limit"`
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

// Normalize applies defaults and trims input. It is safe to call repeatedly.
func (c *Config) Normalize() {
	if c.Country == "" {
		c.Country = DefaultCountry
	}
	c.Country = strings.ToLower(strings.TrimSpace(c.Country))
	if c.SearchLimit <= 0 {
		c.SearchLimit = DefaultSearchLimit
	}
	if c.SearchLimit > DefaultSearchLimit {
		c.SearchLimit = DefaultSearchLimit
	}

	// Split keywords on commas/semicolons/newlines (so a comma-separated list
	// pasted from App Store Connect becomes individual terms), trim, drop
	// empties, and de-duplicate case-insensitively while preserving order.
	sep := func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\r' }
	seen := make(map[string]bool)
	var cleaned []string
	for _, raw := range c.Keywords {
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
			cleaned = append(cleaned, k)
		}
	}
	c.Keywords = cleaned
}

// Validate checks that the config is usable.
func (c *Config) Validate() error {
	if c.AppID <= 0 {
		return fmt.Errorf("config: app_id must be a positive number")
	}
	if len(c.Country) != 2 {
		return fmt.Errorf("config: country must be a two-letter code, got %q", c.Country)
	}
	if len(c.Keywords) == 0 {
		return fmt.Errorf("config: at least one keyword is required")
	}
	return nil
}
