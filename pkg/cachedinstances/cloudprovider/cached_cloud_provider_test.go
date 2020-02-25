package cloudprovider

import (
	"context"
	"testing"
	"time"

	"github.com/ash2k/stager/wait"
	"github.com/atlassian/gostatsd"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
)

func TestCloudHandlerExpirationAndRefresh(t *testing.T) {
	// These still use a real clock, which means they're more susceptible to
	// CPU load triggering a race condition, therefore there's no t.Parallel()
	t.Run("4.3.2.1", func(t *testing.T) {
		testExpireAndRefresh(t, "4.3.2.1", func(h *CloudHandler) {
			e := se1()
			h.DispatchEvent(context.Background(), &e)
		})
	})
	t.Run("1.2.3.4", func(t *testing.T) {
		testExpireAndRefresh(t, "1.2.3.4", func(h *CloudHandler) {
			m := sm1()
			h.DispatchMetrics(context.Background(), []*gostatsd.Metric{&m})
		})
	})
}

func testExpireAndRefresh(t *testing.T, expectedIp gostatsd.IP, f func(*CloudHandler)) {
	fp := &fakeProviderIP{}
	counting := &countingHandler{}
	/*
		Note: lookup reads in to a batch for up to 10ms

		T+0: IP is sent to lookup
		T+10: lookup is performed, cached with eviction time = T+10+50=60, refresh time = T+10+10=20
		T+11: refresh loop, nothing to do
		T+20: cache entry passes refresh time
		T+22: refresh loop, cache item is dispatched for refreshing
		T+32: cache lookup is performed, eviction time is unchanged, refresh time = T+32+10=42
		T+33: refresh loop, nothing to do
		T+42: cache entry passes refresh time
		T+44: refresh loop, cache item is dispatched for refreshing
		T+54: cache lookup is performed, eviction time is unchanged, refresh time = T+54+10=64
		T+55: refresh loop, nothing to do
		T+60: cache entry passes expiry time
		T+64: cache entry passes refresh time
		T+66: refresh loop, entry is expired
		T+70: sleep completes
	*/
	ci := cloudprovider.NewCachedCloudProvider(logrus.StandardLogger(), rate.NewLimiter(100, 120), fp, gostatsd.CacheOptions{
		CacheRefreshPeriod:        11 * time.Millisecond,
		CacheEvictAfterIdlePeriod: 50 * time.Millisecond,
		CacheTTL:                  10 * time.Millisecond,
		CacheNegativeTTL:          100 * time.Millisecond,
	})
	ch := NewCloudHandler(ci, counting)
	var wg wait.Group
	defer wg.Wait()
	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()
	wg.StartWithContext(ctx, ch.Run)
	f(ch)
	time.Sleep(70 * time.Millisecond) // Should be refreshed couple of times and evicted.

	cancelFunc()
	wg.Wait()

	// Cache might refresh multiple times, ensure it only refreshed with the expected IP
	for _, ip := range fp.ips {
		assert.Equal(t, expectedIp, ip)
	}
	assert.GreaterOrEqual(t, len(fp.ips), 2) // Ensure it does at least 1 lookup + 1 refresh
	assert.Zero(t, len(ch.cache))            // Ensure it eventually expired
}
