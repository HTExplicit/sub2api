package service

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// 固定结算系数，不在请求路径查询或自动刷新汇率。
// Source: https://open.er-api.com/v6/latest/USD
// USD/CNY reference, updated 2026-09-09 00:02:31 UTC.
const internalRateConversionFactor = 6.72825

const (
	internalRateConversionCacheTTL = 60 * time.Second
	internalRateConversionErrorTTL = 5 * time.Second
)

type cachedInternalRateConversion struct {
	enabled   bool
	expiresAt time.Time
}

func parseInternalRateConversionEnabled(value string) bool {
	// 存量安装没有此键时默认开启；显式 false 必须保留。
	return strings.TrimSpace(value) != "false"
}

// IsInternalRateConversionEnabled 仅用于最终用户结算，不参与原始价表及账号成本。
func (s *SettingService) IsInternalRateConversionEnabled(ctx context.Context) bool {
	if s == nil || s.settingRepo == nil {
		return true
	}
	if cached := s.internalRateConversionCache.Load(); cached != nil && time.Now().Before(cached.expiresAt) {
		return cached.enabled
	}

	// 与保存后的刷新共用互斥，避免旧的在途读取覆盖刚保存的开关。
	s.internalRateConversionMu.Lock()
	defer s.internalRateConversionMu.Unlock()
	cached := s.internalRateConversionCache.Load()
	if cached != nil && time.Now().Before(cached.expiresAt) {
		return cached.enabled
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	value, err := s.settingRepo.GetValue(dbCtx, SettingKeyInternalRateConversionEnabled)
	enabled, ttl := true, internalRateConversionCacheTTL
	switch {
	case err == nil:
		enabled = parseInternalRateConversionEnabled(value)
	case errors.Is(err, ErrSettingNotFound):
		// 缺省 true，不为旧安装增加迁移或覆盖已经保存的值。
	default:
		ttl = internalRateConversionErrorTTL
		if cached != nil {
			enabled = cached.enabled
		}
		slog.Warn("internal rate conversion setting unavailable; retaining cached or default value")
	}
	s.internalRateConversionCache.Store(&cachedInternalRateConversion{enabled: enabled, expiresAt: time.Now().Add(ttl)})
	return enabled
}

func (s *SettingService) refreshInternalRateConversionCache(enabled bool) {
	s.internalRateConversionMu.Lock()
	defer s.internalRateConversionMu.Unlock()
	s.internalRateConversionCache.Store(&cachedInternalRateConversion{
		enabled: enabled, expiresAt: time.Now().Add(internalRateConversionCacheTTL),
	})
}

func convertInternalRateAmount(amount float64, enabled bool, scale int32) float64 {
	if !enabled || amount == 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return amount
	}
	return decimal.NewFromFloat(amount).
		Mul(decimal.NewFromFloat(internalRateConversionFactor)).
		Round(scale).InexactFloat64()
}

// prepareInternalRateConversion 在普通/OpenAI 共用结算入口固定最终金额。
// 仅修改 ActualCost；分组倍率、基础分项及账号成本仍保持原来的美元口径。
// 同一费用对象即使进入重试，或此后开关切换，也不会二次转换。
func prepareInternalRateConversion(ctx context.Context, settings *SettingService, cost *CostBreakdown, usageLog *UsageLog) {
	if cost == nil {
		return
	}
	if !cost.internalRateConversionPrepared {
		cost.ActualCost = convertInternalRateAmount(cost.ActualCost, settings.IsInternalRateConversionEnabled(ctx), UsageBillingMonetaryScale)
		cost.internalRateConversionPrepared = true
	}
	if usageLog != nil {
		usageLog.ActualCost = cost.ActualCost
	}
}
