package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func Logger() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: c.Response(), status: http.StatusOK}
			c.SetResponse(rec)
			err := next(c)
			slog.Info("request",
				"method", c.Request().Method,
				"path", c.Request().URL.Path,
				"status", rec.status,
				"duration", time.Since(start).String(),
				"ip", c.RealIP(),
			)
			return err
		}
	}
}
