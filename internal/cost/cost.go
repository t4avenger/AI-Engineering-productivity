// Package cost calculates explainable local estimates from observed token usage.
package cost

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"gopkg.in/yaml.v3"
)

const schemaVersion = "0.1.0"

//go:embed prices/default.yaml
var bundled embed.FS

type Catalog struct {
	SchemaVersion  string        `yaml:"schema_version" json:"schema_version"`
	CatalogVersion string        `yaml:"catalog_version" json:"catalog_version"`
	Currency       string        `yaml:"currency" json:"currency"`
	Records        []PriceRecord `yaml:"records" json:"records"`
}

type PriceRecord struct {
	ID              string           `yaml:"id" json:"id"`
	Provider        string           `yaml:"provider" json:"provider"`
	ModelMatcher    string           `yaml:"model_matcher" json:"model_matcher"`
	EffectiveAt     time.Time        `yaml:"effective_at" json:"effective_at"`
	Source          string           `yaml:"source" json:"source"`
	RatesMicrousdPM map[string]int64 `yaml:"rates_microusd_per_million_tokens" json:"rates_microusd_per_million_tokens"`
}

// Record is immutable calculation provenance persisted with an event.
type Record struct {
	EventID            string           `json:"event_id"`
	SessionID          string           `json:"session_id"`
	CalculatedAt       time.Time        `json:"calculated_at"`
	CalculationVersion string           `json:"calculation_version"`
	CatalogVersion     string           `json:"catalog_version"`
	Currency           string           `json:"currency"`
	Status             string           `json:"status"`
	AmountMicrousd     *int64           `json:"amount_microusd"`
	PriceRecordID      *string          `json:"price_record_id"`
	ObservedTokens     map[string]int64 `json:"observed_tokens"`
}

type Calculator struct{ catalog Catalog }

func LoadDefault(overridePath string) (*Calculator, error) {
	data, err := bundled.ReadFile("prices/default.yaml")
	if err != nil {
		return nil, fmt.Errorf("read bundled price catalog: %w", err)
	}
	catalog, err := decodeCatalog(data)
	if err != nil {
		return nil, err
	}
	if overridePath != "" {
		data, err = os.ReadFile(filepath.Clean(overridePath))
		if err != nil {
			return nil, fmt.Errorf("read pricing override: %w", err)
		}
		override, err := decodeCatalog(data)
		if err != nil {
			return nil, fmt.Errorf("validate pricing override: %w", err)
		}
		if override.Currency != catalog.Currency {
			return nil, errors.New("pricing override currency must match bundled catalog")
		}
		catalog = merge(catalog, override)
	}
	return &Calculator{catalog: catalog}, nil
}

func decodeCatalog(data []byte) (Catalog, error) {
	var c Catalog
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Catalog{}, fmt.Errorf("parse price catalog: %w", err)
	}
	if err := validate(c); err != nil {
		return Catalog{}, err
	}
	return c, nil
}

func validate(c Catalog) error {
	if c.SchemaVersion != schemaVersion {
		return fmt.Errorf("price catalog schema_version must be %s", schemaVersion)
	}
	if c.CatalogVersion == "" || c.Currency != "USD" {
		return errors.New("price catalog requires catalog_version and USD currency")
	}
	seen := map[string]bool{}
	for _, r := range c.Records {
		if err := validateRecord(r); err != nil {
			return err
		}
		if seen[r.ID] {
			return fmt.Errorf("duplicate price record id %q", r.ID)
		}
		seen[r.ID] = true
	}
	return nil
}

func validateRecord(r PriceRecord) error {
	if r.ID == "" || r.Provider == "" || r.ModelMatcher == "" || r.EffectiveAt.IsZero() || r.Source == "" {
		return errors.New("price record requires id, provider, model_matcher, effective_at, and source")
	}
	if strings.Count(r.ModelMatcher, "*") > 1 || (strings.Contains(r.ModelMatcher, "*") && !strings.HasSuffix(r.ModelMatcher, "*")) {
		return fmt.Errorf("invalid model matcher %q", r.ModelMatcher)
	}
	if len(r.RatesMicrousdPM) == 0 {
		return fmt.Errorf("price record %q has no rates", r.ID)
	}
	for category, rate := range r.RatesMicrousdPM {
		if !validCategory(category) || rate < 0 {
			return fmt.Errorf("price record %q has invalid %s rate", r.ID, category)
		}
	}
	return nil
}

func merge(base, override Catalog) Catalog {
	byID := map[string]int{}
	for i, r := range base.Records {
		byID[r.ID] = i
	}
	for _, r := range override.Records {
		if i, ok := byID[r.ID]; ok {
			base.Records[i] = r
		} else {
			base.Records = append(base.Records, r)
		}
	}
	base.CatalogVersion = override.CatalogVersion
	return base
}

func (c *Calculator) Calculate(event canonical.Event) Record {
	r := Record{EventID: event.EventID, SessionID: event.SessionID, CalculatedAt: event.ReceivedAt.UTC(), CalculationVersion: "1", CatalogVersion: c.catalog.CatalogVersion, Currency: c.catalog.Currency, Status: "not_applicable", ObservedTokens: map[string]int64{}}
	model, _ := event.Attributes["model"].(string)
	for input, category := range map[string]string{"input_token_count": "input", "cached_input_token_count": "cached_input", "output_token_count": "output", "reasoning_token_count": "reasoning"} {
		if value, ok := token(event.Attributes[input]); ok {
			r.ObservedTokens[category] = value
		}
	}
	if len(r.ObservedTokens) == 0 {
		return r
	}
	if model == "" {
		r.Status = "unknown_model"
		return r
	}
	price := c.selectPrice(event.Provider, model, event.OccurredAt)
	if price == nil {
		r.Status = "unknown_price"
		return r
	}
	r.PriceRecordID = &price.ID
	amount := int64(0)
	partial := false
	for category, tokens := range r.ObservedTokens {
		rate, ok := price.RatesMicrousdPM[category]
		if !ok {
			partial = true
			continue
		}
		amount += roundedProduct(tokens, rate)
	}
	r.AmountMicrousd = &amount
	if partial {
		r.Status = "partial"
	} else {
		r.Status = "calculated"
	}
	return r
}

func (c *Calculator) selectPrice(provider, model string, at time.Time) *PriceRecord {
	var selected *PriceRecord
	best := -1
	for i := range c.catalog.Records {
		r := &c.catalog.Records[i]
		score, ok := match(r.Provider, provider, r.ModelMatcher, model)
		if !ok || r.EffectiveAt.After(at) {
			continue
		}
		if score > best || (score == best && selected != nil && r.EffectiveAt.After(selected.EffectiveAt)) {
			selected, best = r, score
		}
	}
	return selected
}
func match(provider, value, matcher, model string) (int, bool) {
	if provider != value {
		return 0, false
	}
	if strings.HasSuffix(matcher, "*") {
		p := strings.TrimSuffix(matcher, "*")
		return len(p), strings.HasPrefix(model, p)
	}
	return len(matcher) + 10000, matcher == model
}
func validCategory(s string) bool {
	return s == "input" || s == "cached_input" || s == "output" || s == "reasoning"
}
func token(value any) (int64, bool) {
	switch v := value.(type) {
	case string:
		var n int64
		if _, err := fmt.Sscan(v, &n); err == nil && n >= 0 {
			return n, true
		}
	case float64:
		if v >= 0 && v == float64(int64(v)) {
			return int64(v), true
		}
	case json.Number:
		n, err := v.Int64()
		if err == nil && n >= 0 {
			return n, true
		}
	}
	return 0, false
}
func roundedProduct(tokens, rate int64) int64 {
	n := new(big.Int).Mul(big.NewInt(tokens), big.NewInt(rate))
	n.Add(n, big.NewInt(500000))
	return new(big.Int).Div(n, big.NewInt(1000000)).Int64()
}
