package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const deepseekPricingSource = "https://api-docs.deepseek.com/quick_start/pricing/"

var deepseekPeakHours = [][2]int{{9, 12}, {14, 18}}

// The registry is the single source of DeepSeek identity and official off-peak
// USD rates. Verified 2026-09-16: Pro remains available at Pro rates; the
// previously announced September 14 migration to Flash was withdrawn.
var deepseekPricingRegistry = []struct {
	canonical                string
	aliases                  []string
	input, output, cacheRead float64
}{
	{"deepseek-flash", []string{"deepseek-flash", "deepseek-v4.1-flash", "deepseek-v4-flash", "deepseek-v4-flash-vision-exp", "deepseek-v4-flash-0731"}, 1.5e-7, 6e-7, 3e-9},
	{"deepseek-v4-pro", []string{"deepseek-v4-pro", "deepseek-v4-pro-0813"}, 6.6e-7, 1.98e-6, 2.2e-8},
}

type deepseekPricingIdentity struct {
	CanonicalModel string
	Match          string // exact, alias, or fallback; fallback is never identified pricing
	Source         string
	OfficialPeak   bool
}

// deepseekBillingIdentity accepts only the documented namespace. It does not
// guess equivalence from an arbitrary provider path or a version substring.
func deepseekBillingIdentity(model string, allowFallback bool) (deepseekPricingIdentity, bool) {
	name := strings.ToLower(strings.TrimSpace(model))
	bare := strings.TrimPrefix(name, "deepseek/")
	for _, card := range deepseekPricingRegistry {
		for _, alias := range card.aliases {
			if bare == alias {
				match := "alias"
				if name == card.canonical {
					match = "exact"
				}
				return deepseekPricingIdentity{card.canonical, match, deepseekPricingSource, true}, true
			}
		}
	}
	if allowFallback && strings.HasPrefix(bare, "deepseek-") {
		canonical := "deepseek-flash"
		if strings.HasPrefix(bare, "deepseek-v4-pro") {
			canonical = "deepseek-v4-pro"
		}
		return deepseekPricingIdentity{canonical, "fallback", deepseekPricingSource, true}, true
	}
	return deepseekPricingIdentity{}, false
}

// Explicit configured names keep priority; the remaining names are declared
// aliases of the same card, never inferred versions or arbitrary namespaces.
func deepseekPricingAliases(model string) []string {
	identity, ok := deepseekBillingIdentity(model, false)
	if !ok {
		return nil
	}
	var names []string
	for _, card := range deepseekPricingRegistry {
		if card.canonical != identity.CanonicalModel {
			continue
		}
		for _, alias := range card.aliases {
			names = append(names, alias, "deepseek/"+alias)
		}
	}
	return names
}

func deepseekOfficialPrice(identity deepseekPricingIdentity) *LiteLLMModelPricing {
	for _, card := range deepseekPricingRegistry {
		if card.canonical == identity.CanonicalModel {
			return &LiteLLMModelPricing{
				InputCostPerToken: card.input, OutputCostPerToken: card.output,
				CacheReadInputTokenCost: card.cacheRead, LiteLLMProvider: "deepseek",
				Mode: "chat", SupportsPromptCaching: true, deepseekIdentity: &identity,
			}
		}
	}
	return nil
}

// Called under the catalog read lock. Unknown identities can use an exact
// catalog entry, but never generic basename/date stripping. Family fallback
// belongs to BillingService and cannot satisfy HasIdentifiedTokenPricing.
func (s *PricingService) lookupDeepseekPricingLocked(name string) (*LiteLLMModelPricing, bool) {
	if identity, ok := deepseekBillingIdentity(name, false); ok {
		if entry := s.pricingData[name]; entry != nil {
			return entry, true
		}
		if entry := s.pricingData[identity.CanonicalModel]; entry != nil {
			return entry, true
		}
		return deepseekOfficialPrice(identity), true
	}
	if strings.HasPrefix(lastSegment(name), "deepseek-") {
		return s.pricingData[name], true
	}
	return nil, false
}

// mergeDeepSeekPricing materializes the effective catalog on every load,
// remote sync and hot reload. Admin overrides are applied after official
// cards, from the canonical card to the exact alias, including explicit zero.
func (s *PricingService) mergeDeepSeekPricing(data map[string]*LiteLLMModelPricing) map[string]*LiteLLMModelPricing {
	overrides := s.loadPricingOverrideEntries()
	for _, card := range deepseekPricingRegistry {
		for _, alias := range card.aliases {
			for _, name := range []string{alias, "deepseek/" + alias} {
				identity, _ := deepseekBillingIdentity(name, false)
				body, _ := json.Marshal(deepseekOfficialPrice(identity))
				seen := map[string]bool{}
				for _, key := range []string{card.canonical, alias, name} {
					if seen[key] {
						continue
					}
					seen[key] = true
					patch, ok := overrides[key]
					if !ok {
						continue
					}
					merged, valid := mergePricingOverrideEntry(body, patch)
					if !valid {
						continue
					}
					body = merged
					var fields map[string]json.RawMessage
					_ = json.Unmarshal(patch, &fields)
					for field := range fields {
						if strings.Contains(field, "_cost_") || strings.HasSuffix(field, "_cost") {
							identity.Source = "override"
							identity.OfficialPeak = false
						}
					}
				}
				// Reuse price-field parsing without re-applying file overrides.
				wrapped, _ := json.Marshal(map[string]json.RawMessage{name: body})
				parsed, err := (&PricingService{}).parsePricingData(wrapped)
				if err != nil {
					delete(data, name)
					continue
				}
				entry := parsed[name]
				entry.deepseekIdentity = &identity
				data[name] = entry
			}
		}
	}
	return data
}

// Preserve the broad protocol-family predicate used by Ollama request handling.
// Billing uses deepseekBillingIdentity instead.
func isDeepSeekModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "deepseek-")
}

func deepseekPeakMultiplierAt(at time.Time) float64 {
	local := at.In(time.FixedZone("Asia/Shanghai", 8*3600))
	if local.Weekday() == time.Saturday || local.Weekday() == time.Sunday {
		return 1
	}
	h := local.Hour()
	for _, hours := range deepseekPeakHours {
		if h >= hours[0] && h < hours[1] {
			return 2
		}
	}
	return 1
}

func usesDeepseekOfficialTimePricing(resolved *ResolvedPricing) bool {
	return resolved != nil && resolved.Source != PricingSourceGroup && resolved.Source != PricingSourceChannel &&
		resolved.BasePricing != nil && resolved.BasePricing.deepseekIdentity != nil && resolved.BasePricing.deepseekIdentity.OfficialPeak
}

func deepseekTimePricingSchedule() *TimePricingSchedule {
	schedule := &TimePricingSchedule{Timezone: "Asia/Shanghai", WeekdaysOnly: true}
	for _, hours := range deepseekPeakHours {
		schedule.Periods = append(schedule.Periods, TimePricingPeriod{
			StartTime: fmt.Sprintf("%02d:00", hours[0]), EndTime: fmt.Sprintf("%02d:00", hours[1]), Multiplier: 2,
		})
	}
	return schedule
}

func applyDeepseekTimePricing(pricing *ModelPricing, at time.Time) *ModelPricing {
	if pricing == nil || pricing.deepseekIdentity == nil || !pricing.deepseekIdentity.OfficialPeak {
		return pricing
	}
	multiplier := deepseekPeakMultiplierAt(at)
	if multiplier == 1 {
		return pricing
	}
	copy := *pricing
	copy.InputPricePerToken *= multiplier
	copy.OutputPricePerToken *= multiplier
	copy.CacheReadPricePerToken *= multiplier
	return &copy
}
