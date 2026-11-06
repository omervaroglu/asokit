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

	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
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

	// Trim and drop empty keywords.
	cleaned := c.Keywords[:0]
	for _, k := range c.Keywords {
		if k = strings.TrimSpace(k); k != "" {
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
