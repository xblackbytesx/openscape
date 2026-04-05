package handler

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/openscape/openscape/internal/auth"
	"github.com/openscape/openscape/internal/domain"
	"github.com/openscape/openscape/internal/repository"
	"github.com/openscape/openscape/web/templates/pages"
)

type AdminHandler struct {
	galleries *repository.GalleryStore
	photos    *repository.PhotoStore
	users     *repository.UserStore
}

func NewAdminHandler(galleries *repository.GalleryStore, photos *repository.PhotoStore, users *repository.UserStore) *AdminHandler {
	return &AdminHandler{galleries: galleries, photos: photos, users: users}
}

func (h *AdminHandler) Dashboard(c *echo.Context) error {
	ctx := c.Request().Context()
	user := currentUser(c)

	galleries, err := h.galleries.ListByOwner(ctx, user.ID)
	if err != nil {
		galleries = []*domain.Gallery{}
	}

	return pages.AdminDashboard(user, galleries, csrfToken(c)).Render(ctx, c.Response())
}

func (h *AdminHandler) NewGalleryGet(c *echo.Context) error {
	return pages.AdminGalleryNew(currentUser(c), csrfToken(c), "").Render(c.Request().Context(), c.Response())
}

func (h *AdminHandler) CreateGallery(c *echo.Context) error {
	ctx := c.Request().Context()
	user := currentUser(c)

	title := c.FormValue("title")
	description := c.FormValue("description")
	visibility := domain.GalleryVisibility(c.FormValue("visibility"))
	password := c.FormValue("password")

	if title == "" {
		return pages.AdminGalleryNew(user, csrfToken(c), "Title is required.").Render(ctx, c.Response())
	}
	if !isValidVisibility(visibility) {
		visibility = domain.VisibilityPrivate
	}

	// Generate unique slug
	slug := repository.Slugify(title)
	exists, _ := h.galleries.SlugExists(ctx, slug)
	if exists {
		slug = fmt.Sprintf("%s-%s", slug, randomHex(4))
		exists, _ = h.galleries.SlugExists(ctx, slug)
		if exists {
			slug = fmt.Sprintf("%s-%s", slug, randomHex(4))
		}
	}

	g := &domain.Gallery{
		OwnerID:     user.ID,
		Title:       title,
		Description: description,
		Slug:        slug,
		Visibility:  visibility,
	}

	if visibility == domain.VisibilityUnlistedProtected && password != "" {
		hash, err := auth.HashPassword(password)
		if err == nil {
			g.PasswordHash = &hash
		}
	}

	created, err := h.galleries.Create(ctx, g)
	if err != nil {
		return pages.AdminGalleryNew(user, csrfToken(c), "Could not create gallery. Please try again.").Render(ctx, c.Response())
	}

	return redirect(c, "/admin/galleries/"+created.ID.String())
}

func (h *AdminHandler) ManageGallery(c *echo.Context) error {
	ctx := c.Request().Context()
	user := currentUser(c)

	galleryID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.ErrNotFound
	}

	gallery, err := h.galleries.GetByID(ctx, galleryID)
	if err != nil || gallery == nil {
		return echo.ErrNotFound
	}
	if gallery.OwnerID != user.ID && !user.IsAdmin {
		return echo.ErrForbidden
	}

	photos, err := h.photos.ListByGallery(ctx, galleryID)
	if err != nil {
		photos = []*domain.Photo{}
	}

	members, err := h.galleries.ListMembers(ctx, galleryID)
	if err != nil {
		members = []*domain.GalleryMember{}
	}

	return pages.AdminGalleryManage(user, gallery, photos, members, csrfToken(c), "").Render(ctx, c.Response())
}

func (h *AdminHandler) UpdateGallery(c *echo.Context) error {
	ctx := c.Request().Context()
	user := currentUser(c)

	galleryID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.ErrNotFound
	}

	gallery, err := h.galleries.GetByID(ctx, galleryID)
	if err != nil || gallery == nil {
		return echo.ErrNotFound
	}
	if gallery.OwnerID != user.ID && !user.IsAdmin {
		return echo.ErrForbidden
	}

	title := c.FormValue("title")
	description := c.FormValue("description")
	slug := c.FormValue("slug")
	visibility := domain.GalleryVisibility(c.FormValue("visibility"))
	password := c.FormValue("password")

	if !isValidSlug(slug) {
		slug = ""
	}

	if title == "" || slug == "" {
		photos, _ := h.photos.ListByGallery(ctx, galleryID)
		members, _ := h.galleries.ListMembers(ctx, galleryID)
		return pages.AdminGalleryManage(user, gallery, photos, members, csrfToken(c), "Title and slug are required.").Render(ctx, c.Response())
	}
	if !isValidVisibility(visibility) {
		visibility = domain.VisibilityPrivate
	}

	// Check slug uniqueness (excluding current gallery)
	if slug != gallery.Slug {
		existing, _ := h.galleries.GetBySlug(ctx, slug)
		if existing != nil && existing.ID != gallery.ID {
			photos, _ := h.photos.ListByGallery(ctx, galleryID)
			members, _ := h.galleries.ListMembers(ctx, galleryID)
			return pages.AdminGalleryManage(user, gallery, photos, members, csrfToken(c), "That slug is already taken.").Render(ctx, c.Response())
		}
	}

	gallery.Title = title
	gallery.Description = description
	gallery.Slug = slug
	gallery.Visibility = visibility

	if visibility == domain.VisibilityUnlistedProtected {
		if password != "" {
			hash, err := auth.HashPassword(password)
			if err == nil {
				gallery.PasswordHash = &hash
			}
		}
		// keep existing hash if no new password provided
	} else {
		gallery.PasswordHash = nil
	}

	if err := h.galleries.Update(ctx, gallery); err != nil {
		photos, _ := h.photos.ListByGallery(ctx, galleryID)
		members, _ := h.galleries.ListMembers(ctx, galleryID)
		return pages.AdminGalleryManage(user, gallery, photos, members, csrfToken(c), "Could not update gallery.").Render(ctx, c.Response())
	}

	if isHTMX(c) {
		c.Response().Header().Set("HX-Redirect", "/admin/galleries/"+gallery.ID.String())
		return c.NoContent(http.StatusOK)
	}
	return c.Redirect(http.StatusFound, "/admin/galleries/"+gallery.ID.String())
}

func (h *AdminHandler) DeleteGallery(c *echo.Context) error {
	ctx := c.Request().Context()
	user := currentUser(c)

	galleryID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.ErrNotFound
	}

	gallery, err := h.galleries.GetByID(ctx, galleryID)
	if err != nil || gallery == nil {
		return echo.ErrNotFound
	}
	if gallery.OwnerID != user.ID && !user.IsAdmin {
		return echo.ErrForbidden
	}

	if err := h.galleries.Delete(ctx, galleryID); err != nil {
		return echo.ErrInternalServerError
	}

	return redirect(c, "/admin")
}

func (h *AdminHandler) AddMember(c *echo.Context) error {
	ctx := c.Request().Context()
	user := currentUser(c)

	galleryID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.ErrNotFound
	}

	gallery, err := h.galleries.GetByID(ctx, galleryID)
	if err != nil || gallery == nil || gallery.OwnerID != user.ID {
		return echo.ErrForbidden
	}

	memberEmail := c.FormValue("email")
	role := domain.MemberRole(c.FormValue("role"))
	if role != domain.RoleEditor && role != domain.RoleViewer {
		role = domain.RoleViewer
	}

	member, err := h.users.GetByEmail(ctx, memberEmail)
	if err != nil || member == nil {
		photos, _ := h.photos.ListByGallery(ctx, galleryID)
		members, _ := h.galleries.ListMembers(ctx, galleryID)
		return pages.AdminGalleryManage(user, gallery, photos, members, csrfToken(c), "User not found with that email.").Render(ctx, c.Response())
	}

	if err := h.galleries.AddMember(ctx, galleryID, member.ID, role); err != nil {
		return echo.ErrInternalServerError
	}

	return redirect(c, "/admin/galleries/"+galleryID.String())
}

func (h *AdminHandler) RemoveMember(c *echo.Context) error {
	ctx := c.Request().Context()
	user := currentUser(c)

	galleryID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.ErrNotFound
	}

	gallery, err := h.galleries.GetByID(ctx, galleryID)
	if err != nil || gallery == nil || gallery.OwnerID != user.ID {
		return echo.ErrForbidden
	}

	memberID, err := uuid.Parse(c.Param("uid"))
	if err != nil {
		return echo.ErrNotFound
	}

	if err := h.galleries.RemoveMember(ctx, galleryID, memberID); err != nil {
		return echo.ErrInternalServerError
	}

	if isHTMX(c) {
		return c.NoContent(http.StatusOK)
	}
	return c.Redirect(http.StatusFound, "/admin/galleries/"+galleryID.String())
}

func (h *AdminHandler) SetCoverPhoto(c *echo.Context) error {
	ctx := c.Request().Context()
	user := currentUser(c)

	galleryID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.ErrNotFound
	}

	gallery, err := h.galleries.GetByID(ctx, galleryID)
	if err != nil || gallery == nil || gallery.OwnerID != user.ID {
		return echo.ErrForbidden
	}

	photoID, err := uuid.Parse(c.Param("pid"))
	if err != nil {
		return echo.ErrNotFound
	}

	photo, err := h.photos.GetByID(ctx, photoID)
	if err != nil || photo == nil || photo.GalleryID != galleryID {
		return echo.ErrNotFound
	}

	if err := h.galleries.SetCover(ctx, galleryID, photoID); err != nil {
		return echo.ErrInternalServerError
	}

	if isHTMX(c) {
		return c.NoContent(http.StatusOK)
	}
	return c.Redirect(http.StatusFound, "/admin/galleries/"+galleryID.String())
}

func isValidVisibility(v domain.GalleryVisibility) bool {
	switch v {
	case domain.VisibilityPublic, domain.VisibilityUnlisted,
		domain.VisibilityUnlistedProtected, domain.VisibilityPrivate:
		return true
	}
	return false
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
