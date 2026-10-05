package usage

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

//go:embed prices.json
var defaultPricesJSON []byte

// Price is USD per million tokens for one model.
type Price struct {
	Input          float64 `json:"input"`
	Output         float64 `json:"output"`
	CacheRead      float64 `json:"cache_read"`
	CacheWrite5m   float64 `json:"cache_write_5m,omitempty"`  // default 1.25x input
	CacheWrite1h   float64 `json:"cache_write_1h,omitempty"`  // default 2x input
	FastMultiplier float64 `json:"fast_multiplier,omitempty"` // for fast-mode turns, default 1
}

type priceFile struct {
	Comment string           `json:"_comment,omitempty"`
	Models  map[string]Price `json:"models"`
}

var prices = mustParse(defaultPricesJSON)

func mustParse(data []byte) map[string]Price {
	var f priceFile
	if err := json.Unmarshal(data, &f); err != nil {
		panic("usage: bad embedded prices.json: " + err.Error())
	}
	return f.Models
}

// DefaultPricesJSON returns the built-in price table, as a starting point for overrides.
func DefaultPricesJSON() []byte { return defaultPricesJSON }

// LoadPrices merges a price file over the built-in table. Models in the file
// replace the built-in entry with the same key; others are added.
func LoadPrices(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var f priceFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	merged := make(map[string]Price, len(prices)+len(f.Models))
	for k, v := range prices {
		merged[k] = v
	}
	for k, v := range f.Models {
		if v.Input < 0 || v.Output < 0 || v.CacheRead < 0 {
			return fmt.Errorf("%s: negative price for %s", path, k)
		}
		merged[k] = v
	}
	prices = merged
	return nil
}

// lookup finds the price for a model id by longest matching key prefix.
func lookup(model string) (Price, bool) {
	best, found := "", false
	for k := range prices {
		if strings.HasPrefix(model, k) && len(k) > len(best) {
			best, found = k, true
		}
	}
	return prices[best], found
}

// cost prices one set of token counts. fast applies the model's fast-mode multiplier.
func (p Price) cost(in, out, cacheRead, w5m, w1h int64, fast bool) float64 {
	w5 := p.CacheWrite5m
	if w5 == 0 {
		w5 = p.Input * 1.25
	}
	w1 := p.CacheWrite1h
	if w1 == 0 {
		w1 = p.Input * 2
	}
	c := (float64(in)*p.Input + float64(out)*p.Output + float64(cacheRead)*p.CacheRead +
		float64(w5m)*w5 + float64(w1h)*w1) / 1e6
	if fast && p.FastMultiplier > 0 {
		c *= p.FastMultiplier
	}
	return c
}
