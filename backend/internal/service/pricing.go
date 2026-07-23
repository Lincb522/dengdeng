package service

import (
	"strings"
	"sync"
	"time"

	"dengdeng/internal/model"

	"gorm.io/gorm"
)

// PricingService resolves model names to prices with a short in-memory cache.
// Prices are USD per 1M tokens; costs are returned in micro-USD.
type PricingService struct {
	db    *gorm.DB
	mu    sync.RWMutex
	cache []model.ModelPrice
	until time.Time
}

func NewPricingService(db *gorm.DB) *PricingService {
	return &PricingService{db: db}
}

func (s *PricingService) prices() []model.ModelPrice {
	s.mu.RLock()
	if time.Now().Before(s.until) {
		defer s.mu.RUnlock()
		return s.cache
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Now().Before(s.until) {
		return s.cache
	}
	var rows []model.ModelPrice
	if err := s.db.Find(&rows).Error; err == nil {
		s.cache = rows
		s.until = time.Now().Add(30 * time.Second)
	}
	return s.cache
}

func (s *PricingService) Invalidate() {
	s.mu.Lock()
	s.until = time.Time{}
	s.mu.Unlock()
}

// Match picks the most specific rule: exact name first, then the longest
// matching wildcard prefix ("claude-sonnet-*" beats "claude-*").
func (s *PricingService) Match(modelName string) *model.ModelPrice {
	var best *model.ModelPrice
	bestLen := -1
	for i := range s.prices() {
		p := &s.prices()[i]
		if p.Match == modelName {
			return p
		}
		if strings.HasSuffix(p.Match, "*") {
			prefix := strings.TrimSuffix(p.Match, "*")
			if strings.HasPrefix(modelName, prefix) && len(prefix) > bestLen {
				best, bestLen = p, len(prefix)
			}
		}
	}
	return best
}

type Usage struct {
	InputTokens int64
	// InputIncludesCacheRead is true when the upstream reports cached prompt
	// tokens as part of InputTokens (OpenAI Responses/Chat and Gemini do).
	// Anthropic reports ordinary input separately, so it remains false there.
	InputIncludesCacheRead bool
	OutputTokens           int64
	CacheReadTokens        int64
	CacheWriteTokens       int64
	CacheWrite5mTokens     int64
	CacheWrite1hTokens     int64
	ImageInputTokens       int64
	ImageOutputTokens      int64
	ImageCacheReadTokens   int64
	ImageCount             int64
}

// RatePlan is the pricing snapshot used for one request. Its components are
// already combined with the user multiplier by the gateway. Splitting it here
// means cache discounts and long-cache premiums cannot accidentally affect
// normal input/output billing.
type RatePlan struct {
	Base         float64
	CacheRead    float64
	CacheWrite5m float64
	CacheWrite1h float64
	Image        float64
}

func validMultiplier(v float64) float64 {
	if v <= 0 {
		return 1
	}
	return v
}

func (r RatePlan) normalize() RatePlan {
	r.Base = validMultiplier(r.Base)
	r.CacheRead = validMultiplier(r.CacheRead)
	r.CacheWrite5m = validMultiplier(r.CacheWrite5m)
	r.CacheWrite1h = validMultiplier(r.CacheWrite1h)
	r.Image = validMultiplier(r.Image)
	return r
}

func cacheWritePrice(p *model.ModelPrice, ttl string) float64 {
	switch ttl {
	case "5m":
		if p.CacheWrite5mPrice > 0 {
			return p.CacheWrite5mPrice
		}
	case "1h":
		if p.CacheWrite1hPrice > 0 {
			return p.CacheWrite1hPrice
		}
	}
	return p.CacheWritePrice
}

// CostBreakdown is the immutable pricing result for one usage record. All
// component amounts and TotalMicro are charged micro-USD; RawMicro is the
// catalogue-price total before user, group, cache and effort multipliers.
type CostBreakdown struct {
	TotalMicro          int64
	RawMicro            int64
	InputMicro          int64
	OutputMicro         int64
	CacheReadMicro      int64
	CacheWriteMicro     int64
	ImageMicro          int64
	EffectiveMultiplier float64
	InputUnitPrice      float64
	OutputUnitPrice     float64
	CacheReadUnitPrice  float64
	CacheWriteUnitPrice float64
	CacheWrite5mPrice   float64
	CacheWrite1hPrice   float64
	ImageUnitPrice      float64
}

// Cost returns micro-USD for the given usage after applying its rate plan.
// price(USD/1Mtok) * tokens == micro-USD directly.
func (s *PricingService) Cost(modelName string, u Usage, rates RatePlan) int64 {
	return s.Breakdown(modelName, u, rates).TotalMicro
}

// Breakdown returns both the final charge and the values needed to explain it.
// Integer component amounts are reconciled to TotalMicro so the UI always
// displays a breakdown whose rows add up to the recorded user charge.
func (s *PricingService) Breakdown(modelName string, u Usage, rates RatePlan) CostBreakdown {
	p := s.Match(modelName)
	if p == nil {
		return CostBreakdown{}
	}
	rates = rates.normalize()

	cacheWrite5m := u.CacheWrite5mTokens
	cacheWrite1h := u.CacheWrite1hTokens
	cacheWriteTotal := u.CacheWriteTokens
	if cacheWriteTotal == 0 {
		cacheWriteTotal = cacheWrite5m + cacheWrite1h
	}
	// A provider may return a total together with only a partial TTL breakdown.
	// Bill any unmatched remainder with the legacy/default write price instead
	// of double counting it or silently dropping it.
	cacheWriteOther := cacheWriteTotal - cacheWrite5m - cacheWrite1h
	if cacheWriteOther < 0 {
		cacheWriteOther = 0
	}

	inputTokens := u.InputTokens
	if u.InputIncludesCacheRead && u.CacheReadTokens > 0 {
		// A cached prompt segment cannot be charged both at the regular input
		// price and at the discounted cache-read price. Guard malformed upstream
		// usage so a cache detail never makes normal input negative.
		inputTokens -= u.CacheReadTokens
		if inputTokens < 0 {
			inputTokens = 0
		}
	}

	inputRaw := float64(inputTokens) * p.InputPrice
	outputRaw := float64(u.OutputTokens) * p.OutputPrice
	cacheReadRaw := float64(u.CacheReadTokens) * p.CacheReadPrice
	cacheWriteRaw := float64(cacheWrite5m)*cacheWritePrice(p, "5m") +
		float64(cacheWrite1h)*cacheWritePrice(p, "1h") +
		float64(cacheWriteOther)*p.CacheWritePrice
	inputCharged := inputRaw * rates.Base
	outputCharged := outputRaw * rates.Base
	cacheReadCharged := cacheReadRaw * rates.CacheRead
	cacheWriteCharged := float64(cacheWrite5m)*cacheWritePrice(p, "5m")*rates.CacheWrite5m +
		float64(cacheWrite1h)*cacheWritePrice(p, "1h")*rates.CacheWrite1h +
		float64(cacheWriteOther)*p.CacheWritePrice*rates.CacheWrite5m

	var imageRaw, imageCharged float64
	// A fixed per-image price is deliberately an override, rather than an
	// addition, so operators do not charge image-token and image-unit prices
	// for the same generated image.
	if p.ImagePricePerImage > 0 && u.ImageCount > 0 {
		imageRaw = float64(u.ImageCount) * p.ImagePricePerImage * 1_000_000
		imageCharged = imageRaw * rates.Image
	} else {
		imageRaw = float64(u.ImageInputTokens)*p.ImageInputPrice +
			float64(u.ImageOutputTokens)*p.ImageOutputPrice +
			float64(u.ImageCacheReadTokens)*p.ImageCacheReadPrice
		imageCharged = imageRaw * rates.Image
	}

	raw := inputRaw + outputRaw + cacheReadRaw + cacheWriteRaw + imageRaw
	charged := inputCharged + outputCharged + cacheReadCharged + cacheWriteCharged + imageCharged
	result := CostBreakdown{
		TotalMicro:          int64(charged),
		RawMicro:            int64(raw),
		InputMicro:          int64(inputCharged),
		OutputMicro:         int64(outputCharged),
		CacheReadMicro:      int64(cacheReadCharged),
		CacheWriteMicro:     int64(cacheWriteCharged),
		ImageMicro:          int64(imageCharged),
		InputUnitPrice:      p.InputPrice,
		OutputUnitPrice:     p.OutputPrice,
		CacheReadUnitPrice:  p.CacheReadPrice,
		CacheWriteUnitPrice: p.CacheWritePrice,
		CacheWrite5mPrice:   cacheWritePrice(p, "5m"),
		CacheWrite1hPrice:   cacheWritePrice(p, "1h"),
		ImageUnitPrice:      p.ImagePricePerImage,
	}
	if raw > 0 {
		result.EffectiveMultiplier = charged / raw
	}
	// Flooring individual positive components can lose a few micro-dollars
	// compared with flooring their sum. Assign that harmless remainder to the
	// first applicable component so the breakdown remains arithmetically exact.
	remainder := result.TotalMicro - result.InputMicro - result.OutputMicro -
		result.CacheReadMicro - result.CacheWriteMicro - result.ImageMicro
	switch {
	case remainder == 0:
	case inputCharged > 0:
		result.InputMicro += remainder
	case outputCharged > 0:
		result.OutputMicro += remainder
	case cacheReadCharged > 0:
		result.CacheReadMicro += remainder
	case cacheWriteCharged > 0:
		result.CacheWriteMicro += remainder
	default:
		result.ImageMicro += remainder
	}
	return result
}
