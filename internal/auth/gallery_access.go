package auth

import (
	"context"

	"github.com/openscape/openscape/internal/domain"
)

// GalleryAccessResult describes whether a user can view a gallery, plus how
// to recover if they can't (redirect to unlock / login). Callers map these
// flags onto their own UX (page redirect for the gallery view, plain HTTP
// status for the upload-serve handler).
type GalleryAccessResult struct {
	Allowed        bool
	CanEdit        bool
	RequiresUnlock bool // password-protected gallery with no/expired session cookie
	RequiresLogin  bool // private gallery, no user
	Forbidden      bool // private gallery, user not a member
}

// CheckGalleryAccess decides whether `user` (which may be nil) can view
// `gallery`. cookieValue is the raw value of the gallery's unlock cookie if
// present (empty otherwise). validUnlockToken is invoked only for protected
// galleries — it should return true when the cookie value matches a live
// session for this gallery. memberLookup is invoked only for private
// galleries — it should return the user's membership row, or nil.
//
// Both callbacks take the same context for cancellation propagation.
func CheckGalleryAccess(
	ctx context.Context,
	gallery *domain.Gallery,
	user *domain.User,
	cookieValue string,
	validUnlockToken func(ctx context.Context, token string) bool,
	memberLookup func(ctx context.Context) *domain.GalleryMember,
) GalleryAccessResult {
	if gallery == nil {
		return GalleryAccessResult{Forbidden: true}
	}

	if user != nil && gallery.OwnerID == user.ID {
		return GalleryAccessResult{Allowed: true, CanEdit: true}
	}
	if user != nil && user.IsAdmin {
		return GalleryAccessResult{Allowed: true}
	}

	switch gallery.Visibility {
	case domain.VisibilityPublic, domain.VisibilityUnlisted:
		return GalleryAccessResult{Allowed: true}

	case domain.VisibilityUnlistedProtected:
		if cookieValue == "" || !validUnlockToken(ctx, cookieValue) {
			return GalleryAccessResult{RequiresUnlock: true}
		}
		return GalleryAccessResult{Allowed: true}

	case domain.VisibilityPrivate:
		if user == nil {
			return GalleryAccessResult{RequiresLogin: true}
		}
		member := memberLookup(ctx)
		if member == nil {
			return GalleryAccessResult{Forbidden: true}
		}
		canEdit := member.Role == domain.RoleEditor
		return GalleryAccessResult{Allowed: true, CanEdit: canEdit}
	}
	return GalleryAccessResult{Forbidden: true}
}
