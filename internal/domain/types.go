package domain

import (
	"time"

	"github.com/google/uuid"
)

type GalleryVisibility string

const (
	VisibilityPublic            GalleryVisibility = "public"
	VisibilityUnlisted          GalleryVisibility = "unlisted"
	VisibilityUnlistedProtected GalleryVisibility = "unlisted_protected"
	VisibilityPrivate           GalleryVisibility = "private"
)

type MemberRole string

const (
	RoleEditor MemberRole = "editor"
	RoleViewer MemberRole = "viewer"
)

type User struct {
	ID           uuid.UUID
	Username     string
	Email        string
	PasswordHash string
	IsAdmin      bool
	CreatedAt    time.Time
}

type Gallery struct {
	ID           uuid.UUID
	OwnerID      uuid.UUID
	Title        string
	Description  string
	Slug         string
	Visibility   GalleryVisibility
	PasswordHash *string
	CoverPhotoID *uuid.UUID
	CreatedAt    time.Time
	UpdatedAt    time.Time

	// Populated by joins
	PhotoCount int
	CoverThumb string
}

func (g *Gallery) IsPubliclyViewable() bool {
	return g.Visibility == VisibilityPublic || g.Visibility == VisibilityUnlisted
}

func (g *Gallery) RequiresPassword() bool {
	return g.Visibility == VisibilityUnlistedProtected
}

func (g *Gallery) IsPrivate() bool {
	return g.Visibility == VisibilityPrivate
}

type GalleryMember struct {
	GalleryID uuid.UUID
	UserID    uuid.UUID
	Role      MemberRole
	Username  string
	Email     string
}

type Photo struct {
	ID             uuid.UUID
	GalleryID      uuid.UUID
	UploadedBy     uuid.UUID
	Title          string
	Description    string
	Filename       string
	StoragePath    string
	ThumbPath      string
	Width          *int
	Height         *int
	FileSize       *int64
	MimeType       string
	Is360          bool
	ProjectionType *string
	ExifData       map[string]any
	CapturedAt     *time.Time
	SortOrder      int
	CreatedAt      time.Time
}

// ThumbURL returns the URL to the thumbnail image.
// ThumbPath is stored as e.g. "galleryID/thumbs/photoID_thumb.jpg"
func (p *Photo) ThumbURL() string {
	return "/uploads/" + p.ThumbPath
}

// OriginalURL returns the URL to the original image.
// StoragePath is stored as e.g. "galleryID/originals/photoID.jpg"
func (p *Photo) OriginalURL() string {
	return "/uploads/" + p.StoragePath
}

func (p *Photo) AspectClass() string {
	if p.Is360 {
		return "panoramic"
	}
	return "standard"
}

func (p *Photo) DisplayTitle() string {
	if p.Title != "" {
		return p.Title
	}
	return p.Filename
}

type GallerySession struct {
	Token     string
	GalleryID uuid.UUID
	CreatedAt time.Time
	ExpiresAt time.Time
}
