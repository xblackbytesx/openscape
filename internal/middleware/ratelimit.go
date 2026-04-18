package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
	"golang.org/x/time/rate"
)

type ipLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type RateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*ipLimiter
	r        rate.Limit
	b        int
	ticker   *time.Ticker
}

func NewRateLimiter(requestsPerMinute float64, burst int) *RateLimiter {
	rl := &RateLimiter{
		limiters: make(map[string]*ipLimiter),
		r:        rate.Limit(requestsPerMinute / 60.0),
		b:        burst,
		ticker:   time.NewTicker(5 * time.Minute),
	}
	go func() {
		for range rl.ticker.C {
			rl.evict()
		}
	}()
	return rl
}

// Stop releases the background eviction goroutine.
func (rl *RateLimiter) Stop() { rl.ticker.Stop() }

func (rl *RateLimiter) evict() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := time.Now().Add(-10 * time.Minute)
	for ip, l := range rl.limiters {
		if l.lastSeen.Before(cutoff) {
			delete(rl.limiters, ip)
		}
	}
}

func (rl *RateLimiter) getLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	l, ok := rl.limiters[ip]
	if !ok {
		l = &ipLimiter{limiter: rate.NewLimiter(rl.r, rl.b)}
		rl.limiters[ip] = l
	}
	l.lastSeen = time.Now()
	return l.limiter
}

func (rl *RateLimiter) Middleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			limiter := rl.getLimiter(c.RealIP())
			if !limiter.Allow() {
				return c.JSON(http.StatusTooManyRequests, map[string]string{
					"error": "Too many requests. Please try again later.",
				})
			}
			return next(c)
		}
	}
}
