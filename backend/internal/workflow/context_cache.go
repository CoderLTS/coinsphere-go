package workflow

import (
	"context"
	"fmt"
	"sync"
	"time"

	"coinsphere/backend/plugin/sdk"
)

// contextCache 实现 WorkflowContext 的缓存功能
type contextCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
}

type cacheEntry struct {
	value     interface{}
	expiresAt time.Time
}

// newContextCache 创建新的上下文缓存
func newContextCache() *contextCache {
	return &contextCache{
		entries: make(map[string]*cacheEntry),
	}
}

// Get 获取缓存值
func (c *contextCache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[key]
	if !exists {
		return nil, false
	}

	// 检查是否过期
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		return nil, false
	}

	return entry.value, true
}

// Set 设置缓存值（默认永不过期）
func (c *contextCache) Set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = &cacheEntry{
		value:     value,
		expiresAt: time.Time{}, // 零值表示永不过期
	}
}

// SetWithTTL 设置带 TTL 的缓存值
func (c *contextCache) SetWithTTL(key string, value interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = &cacheEntry{
		value:     value,
		expiresAt: time.Now().Add(ttl),
	}
}

// Clear 清空所有缓存
func (c *contextCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*cacheEntry)
}

// Size 返回缓存条目数量
func (c *contextCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.entries)
}

// cleanupExpired 清理过期缓存
func (c *contextCache) cleanupExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, entry := range c.entries {
		if !entry.expiresAt.IsZero() && now.After(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
}

// workflowContextImpl 实现 WorkflowContext 接口，提供缓存支持
type workflowContextImpl struct {
	ctx        context.Context
	secrets    map[string]string
	logger     sdk.Logger
	cache      *contextCache
	marketData sdk.MarketDataRegistry
}

// NewWorkflowContext 创建新的工作流上下文
func NewWorkflowContext(ctx context.Context, logger sdk.Logger, marketData sdk.MarketDataRegistry) sdk.WorkflowContext {
	return &workflowContextImpl{
		ctx:        ctx,
		secrets:    make(map[string]string),
		logger:     logger,
		cache:      newContextCache(),
		marketData: marketData,
	}
}

// GetSecret 实现 WorkflowContext.GetSecret
func (w *workflowContextImpl) GetSecret(key string) (string, error) {
	if value, ok := w.secrets[key]; ok {
		return value, nil
	}
	return "", sdk.ErrSecretNotFound
}

// LogInfo 实现 WorkflowContext.LogInfo
func (w *workflowContextImpl) LogInfo(message string, fields map[string]interface{}) {
	if w.logger != nil {
		w.logger.Info(message, fields)
	}
}

// GetCache 实现 WorkflowContext.GetCache
func (w *workflowContextImpl) GetCache(key string) (interface{}, bool) {
	return w.cache.Get(key)
}

// SetCache 实现 WorkflowContext.SetCache
func (w *workflowContextImpl) SetCache(key string, value interface{}) {
	w.cache.Set(key, value)
}

// SetCacheWithTTL 实现 WorkflowContext.SetCacheWithTTL
func (w *workflowContextImpl) SetCacheWithTTL(key string, value interface{}, ttl time.Duration) {
	w.cache.SetWithTTL(key, value, ttl)
}

// LoadCandles 实现 WorkflowContext.LoadCandles
func (w *workflowContextImpl) LoadCandles(venue string, query sdk.CandleQuery) ([]sdk.Candle, error) {
	// 构建缓存键
	cacheKey := fmt.Sprintf("candles:%s:%s:%s:%s:%d", venue, query.Market, query.Instrument, query.Interval, query.Limit)

	// 检查缓存
	if cached, ok := w.cache.Get(cacheKey); ok {
		if candles, ok := cached.([]sdk.Candle); ok {
			w.LogInfo("cache_hit", map[string]interface{}{
				"cache_key": cacheKey,
				"count":     len(candles),
			})
			return candles, nil
		}
	}

	// 缓存未命中，从市场数据加载
	w.LogInfo("cache_miss", map[string]interface{}{
		"cache_key": cacheKey,
	})

	candles, err := w.marketData.GetCandles(w.ctx, venue, query.Market, query.Instrument, query.Interval, query.Limit)
	if err != nil {
		return nil, err
	}

	// 写入缓存（默认 5 分钟 TTL）
	w.cache.SetWithTTL(cacheKey, candles, 5*time.Minute)

	return candles, nil
}

// LoadCandlesBatch 实现 WorkflowContext.LoadCandlesBatch
func (w *workflowContextImpl) LoadCandlesBatch(queries []sdk.CandleQueryWithVenue) (map[string][]sdk.Candle, error) {
	result := make(map[string][]sdk.Candle)
	var uncachedQueries []sdk.CandleQueryWithVenue

	// 先检查缓存
	for _, query := range queries {
		cacheKey := query.CacheKey()
		if cached, ok := w.cache.Get(cacheKey); ok {
			if candles, ok := cached.([]sdk.Candle); ok {
				result[cacheKey] = candles
				continue
			}
		}
		uncachedQueries = append(uncachedQueries, query)
	}

	w.LogInfo("batch_cache_check", map[string]interface{}{
		"total":    len(queries),
		"cached":   len(result),
		"uncached": len(uncachedQueries),
		"hit_rate": float64(len(result)) / float64(len(queries)),
	})

	// 批量加载未缓存的数据
	if len(uncachedQueries) > 0 {
		// TODO: 实现真正的批量加载优化
		// 目前先串行加载
		for _, query := range uncachedQueries {
			candles, err := w.LoadCandles(query.Venue, query.CandleQuery)
			if err != nil {
				return nil, err
			}
			result[query.CacheKey()] = candles
		}
	}

	return result, nil
}

// ClearCache 清空所有缓存（用于测试）
func (w *workflowContextImpl) ClearCache() {
	w.cache.Clear()
}

// CacheSize 返回缓存大小（用于测试和监控）
func (w *workflowContextImpl) CacheSize() int {
	return w.cache.Size()
}
