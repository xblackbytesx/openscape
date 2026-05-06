package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/labstack/echo/v5"
	echomiddleware "github.com/labstack/echo/v5/middleware"
	"github.com/openscape/openscape/internal/auth"
	"github.com/openscape/openscape/internal/config"
	"github.com/openscape/openscape/internal/db"
	"github.com/openscape/openscape/internal/handler"
	appmiddleware "github.com/openscape/openscape/internal/middleware"
	"github.com/openscape/openscape/internal/media"
	"github.com/openscape/openscape/internal/repository"
	"github.com/openscape/openscape/internal/worker"
)

func main() {
	// ── Config ──────────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config error", "error", err)
		os.Exit(1)
	}

	// ── Database ─────────────────────────────────────────────────────────────
	pool, err := db.NewPool(cfg.DatabaseURL)
	if err != nil {
		slog.Error("database connect failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := db.RunMigrations(cfg.DatabaseURL); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("migrations applied")

	// ── Stores ───────────────────────────────────────────────────────────────
	userStore          := repository.NewUserStore(pool)
	galleryStore       := repository.NewGalleryStore(pool)
	photoStore         := repository.NewPhotoStore(pool)
	gallerySessionStore := repository.NewGallerySessionStore(pool)

	// ── Services ─────────────────────────────────────────────────────────────
	processor := media.NewProcessor(cfg.UploadsPath)
	workerPool := worker.New(processor, photoStore, 0, 0)

	tusURLPrefix := "/admin/galleries/tus/"
	tusHandler, err := handler.NewTusHandler(tusURLPrefix, cfg.UploadsPath, galleryStore, photoStore, processor, workerPool)
	if err != nil {
		slog.Error("tus init failed", "error", err)
		os.Exit(1)
	}

	// ── Background maintenance ────────────────────────────────────────────────
	cleanupTicker := time.NewTicker(6 * time.Hour)
	cleanupDone := make(chan struct{})
	go func() {
		for {
			select {
			case <-cleanupTicker.C:
				_ = gallerySessionStore.DeleteExpired(context.Background())
			case <-cleanupDone:
				return
			}
		}
	}()

	// ── Auth ─────────────────────────────────────────────────────────────────
	auth.InitStore(cfg.SessionSecret, cfg.SecureCookies)

	// ── Handlers ─────────────────────────────────────────────────────────────
	setupHandler   := handler.NewSetupHandler(userStore)
	authHandler    := handler.NewAuthHandler(userStore, cfg.AllowRegistration)
	homeHandler    := handler.NewHomeHandler(galleryStore)
	galleryHandler := handler.NewGalleryHandler(galleryStore, photoStore, gallerySessionStore, cfg.SecureCookies)
	adminHandler   := handler.NewAdminHandler(galleryStore, photoStore, userStore)
	uploadHandler  := handler.NewUploadHandler(galleryStore, photoStore, processor, workerPool, cfg.MaxUploadMB)
	usersHandler   := handler.NewUsersHandler(userStore)

	// ── Rate limiters ────────────────────────────────────────────────────────
	authLimiter    := appmiddleware.NewRateLimiter(5, 5)   // 5 req/min
	unlockLimiter  := appmiddleware.NewRateLimiter(5, 5)

	// ── Echo ─────────────────────────────────────────────────────────────────
	e := echo.New()
	defaultErrHandler := echo.DefaultHTTPErrorHandler(false)
	e.HTTPErrorHandler = func(c *echo.Context, err error) {
		he, ok := err.(*echo.HTTPError)
		if ok && he.Code == http.StatusForbidden && c.Request().Header.Get("HX-Request") == "true" {
			c.Response().Header().Set("HX-Redirect", c.Request().RequestURI)
			_ = c.NoContent(http.StatusForbidden)
			return
		}
		defaultErrHandler(c, err)
	}

	// Global middleware
	e.Use(echomiddleware.Recover())
	e.Use(appmiddleware.Logger())
	e.Use(echomiddleware.SecureWithConfig(echomiddleware.SecureConfig{
		XSSProtection:         "1; mode=block",
		ContentTypeNosniff:    "nosniff",
		XFrameOptions:         "SAMEORIGIN",
		HSTSMaxAge:            31536000,
		ContentSecurityPolicy: "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; worker-src blob: 'self'; connect-src 'self';",
	}))

	// CSRF protection (Echo-native middleware)
	e.Use(echomiddleware.CSRFWithConfig(echomiddleware.CSRFConfig{
		TokenLookup:    "form:_csrf,header:X-CSRF-Token",
		CookieName:     "_csrf",
		CookiePath:     "/",
		CookieHTTPOnly: true,
		CookieSecure:   cfg.SecureCookies,
		CookieSameSite: http.SameSiteStrictMode,
	}))

	// First-run redirect to /setup
	e.Use(handler.CheckSetup(userStore))

	// Static files. Vendor JS (HTMX, PSV, tus) and CSS rarely change between
	// deploys, so we cache aggressively. http.FileServer(http.Dir(...)) is
	// the canonical traversal-safe path here; appending the URL path to a
	// string would let "../etc/passwd" escape the root.
	staticFS := http.StripPrefix("/static", http.FileServer(http.Dir("web/static")))
	e.GET("/static/*", func(c *echo.Context) error {
		c.Response().Header().Set("Cache-Control", "public, max-age=2592000")
		staticFS.ServeHTTP(c.Response(), c.Request())
		return nil
	})

	// ── Setup ────────────────────────────────────────────────────────────────
	e.GET("/setup", setupHandler.Get)
	e.POST("/setup", setupHandler.Post)

	// ── Auth ─────────────────────────────────────────────────────────────────
	e.GET("/login",    authHandler.LoginGet,    authLimiter.Middleware())
	e.POST("/login",   authHandler.LoginPost,   authLimiter.Middleware())
	e.GET("/register", authHandler.RegisterGet)
	e.POST("/register", authHandler.RegisterPost, authLimiter.Middleware())
	e.POST("/logout",  authHandler.Logout)

	// ── Home ─────────────────────────────────────────────────────────────────
	e.GET("/", homeHandler.Home, appmiddleware.InjectUser(userStore))

	// ── Public gallery viewer ─────────────────────────────────────────────────
	// Unlock (no gallery access check — user must prove password first)
	e.GET("/g/:slug/unlock",  galleryHandler.UnlockGet,  unlockLimiter.Middleware())
	e.POST("/g/:slug/unlock", galleryHandler.UnlockPost, unlockLimiter.Middleware())

	// Gallery viewer routes (access-checked)
	gv := e.Group("/g/:slug",
		appmiddleware.InjectUser(userStore),
		appmiddleware.CheckGalleryAccess(galleryStore, gallerySessionStore),
	)
	gv.GET("",          galleryHandler.View)
	gv.GET("/photo/:id", galleryHandler.PhotoView)

	// ── Upload serving (access-checked by gallery_id) ────────────────────────
	e.GET("/uploads/:gallery_id/*",
		handler.ServeUpload(processor, galleryStore, gallerySessionStore),
		appmiddleware.InjectUser(userStore),
	)

	// ── Admin (requires auth) ─────────────────────────────────────────────────
	admin := e.Group("/admin",
		appmiddleware.InjectUser(userStore),
		appmiddleware.RequireAuth(),
	)
	admin.GET("",  adminHandler.Dashboard)

	admin.GET("/galleries/new",    adminHandler.NewGalleryGet)
	admin.POST("/galleries",       adminHandler.CreateGallery)
	admin.GET("/galleries/:id",    adminHandler.ManageGallery)
	admin.PUT("/galleries/:id",    adminHandler.UpdateGallery)
	admin.DELETE("/galleries/:id", adminHandler.DeleteGallery)

	admin.POST("/galleries/:id/photos",          uploadHandler.Upload)
	admin.GET("/galleries/:id/photos/:pid/status", uploadHandler.PhotoStatus)
	admin.DELETE("/galleries/:id/photos/:pid",   uploadHandler.DeletePhoto)
	admin.PUT("/galleries/:id/photos/:pid",      uploadHandler.UpdatePhotoMeta)
	admin.POST("/galleries/:id/photos/reorder",       uploadHandler.ReorderPhotos)
	admin.POST("/galleries/:id/photos/sort-by-date",  uploadHandler.SortByDate)
	admin.POST("/galleries/:id/cover/:pid",      adminHandler.SetCoverPhoto)

	// tus.io resumable upload endpoint. tusd handles all methods (POST/HEAD/
	// PATCH/DELETE/OPTIONS) at this prefix and below; auth check still runs.
	for _, m := range []string{http.MethodPost, http.MethodHead, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		admin.Add(m, "/galleries/tus", tusHandler.Mount)
		admin.Add(m, "/galleries/tus/*", tusHandler.Mount)
	}

	admin.GET("/galleries/:id/members",        adminHandler.ManageGallery) // renders same page
	admin.POST("/galleries/:id/members",       adminHandler.AddMember)
	admin.DELETE("/galleries/:id/members/:uid", adminHandler.RemoveMember)

	// Users (admin-only)
	adminOnly := e.Group("/admin",
		appmiddleware.InjectUser(userStore),
		appmiddleware.RequireAuth(),
		appmiddleware.RequireAdmin(),
	)
	adminOnly.GET("/users",        usersHandler.List)
	adminOnly.POST("/users",       usersHandler.Create)
	adminOnly.DELETE("/users/:id", usersHandler.Delete)

	// ── Start ─────────────────────────────────────────────────────────────────
	addr := fmt.Sprintf(":%s", cfg.Port)
	slog.Info("starting openscape", "addr", addr)

	// Body read/write timeouts are intentionally zero so a slow upload is not
	// killed by Go itself — the upload handler streams parts to disk and a
	// stalled request can be cancelled via context. ReadHeaderTimeout protects
	// against slowloris on the request line/headers; IdleTimeout reaps idle
	// keep-alive connections so the connection pool can recycle.
	srv := &http.Server{
		Addr:              addr,
		Handler:           e,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	shutdownCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	case <-shutdownCtx.Done():
		slog.Info("shutdown signal received")
	}

	// Stop accepting new HTTP requests but let in-flight ones finish.
	// Echo v5 doesn't expose Shutdown directly — we drive the underlying
	// http.Server, which already has Echo wired in as its Handler.
	graceCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(graceCtx); err != nil {
		slog.Warn("http shutdown failed", "error", err)
	}

	// Tear down background goroutines.
	cleanupTicker.Stop()
	close(cleanupDone)
	authLimiter.Stop()
	unlockLimiter.Stop()

	// Drain in-flight processing jobs (or give up after a deadline).
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer drainCancel()
	workerPool.Shutdown(drainCtx)

	slog.Info("shutdown complete")
}
