package main

import (
	"context"
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

	// ── Background maintenance ────────────────────────────────────────────────
	cleanupTicker := time.NewTicker(6 * time.Hour)
	go func() {
		for range cleanupTicker.C {
			_ = gallerySessionStore.DeleteExpired(context.Background())
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
	uploadHandler  := handler.NewUploadHandler(galleryStore, photoStore, processor, cfg.MaxUploadMB)
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

	// Static files
	e.Static("/static", "web/static")

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
	admin.DELETE("/galleries/:id/photos/:pid",   uploadHandler.DeletePhoto)
	admin.PUT("/galleries/:id/photos/:pid",      uploadHandler.UpdatePhotoMeta)
	admin.POST("/galleries/:id/photos/reorder",       uploadHandler.ReorderPhotos)
	admin.POST("/galleries/:id/photos/sort-by-date",  uploadHandler.SortByDate)
	admin.POST("/galleries/:id/cover/:pid",      adminHandler.SetCoverPhoto)

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

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := e.Start(addr); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-sigCtx.Done()
	slog.Info("shutting down...")
	cleanupTicker.Stop()
	authLimiter.Stop()
	unlockLimiter.Stop()

	shutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := e.Shutdown(shutCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
}
