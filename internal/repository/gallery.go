package repository

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openscape/openscape/internal/domain"
)

type GalleryStore struct {
	pool *pgxpool.Pool
}

func NewGalleryStore(pool *pgxpool.Pool) *GalleryStore {
	return &GalleryStore{pool: pool}
}

var nonSlugRe = regexp.MustCompile(`[^a-z0-9-]`)

func Slugify(title string) string {
	s := strings.ToLower(title)
	s = strings.ReplaceAll(s, " ", "-")
	s = nonSlugRe.ReplaceAllString(s, "")
	s = regexp.MustCompile(`-+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = s[:60]
	}
	if s == "" {
		s = "gallery"
	}
	return s
}

func (s *GalleryStore) Create(ctx context.Context, g *domain.Gallery) (*domain.Gallery, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO galleries (owner_id, title, description, slug, visibility, password_hash)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_at, updated_at`,
		g.OwnerID, g.Title, g.Description, g.Slug, g.Visibility, g.PasswordHash,
	)
	if err := row.Scan(&g.ID, &g.CreatedAt, &g.UpdatedAt); err != nil {
		return nil, fmt.Errorf("create gallery: %w", err)
	}
	return g, nil
}

func (s *GalleryStore) GetBySlug(ctx context.Context, slug string) (*domain.Gallery, error) {
	g := &domain.Gallery{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, owner_id, title, description, slug, visibility, password_hash, cover_photo_id, created_at, updated_at
		 FROM galleries WHERE slug = $1`, slug,
	).Scan(&g.ID, &g.OwnerID, &g.Title, &g.Description, &g.Slug, &g.Visibility,
		&g.PasswordHash, &g.CoverPhotoID, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get gallery by slug: %w", err)
	}
	return g, nil
}

func (s *GalleryStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.Gallery, error) {
	g := &domain.Gallery{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, owner_id, title, description, slug, visibility, password_hash, cover_photo_id, created_at, updated_at
		 FROM galleries WHERE id = $1`, id,
	).Scan(&g.ID, &g.OwnerID, &g.Title, &g.Description, &g.Slug, &g.Visibility,
		&g.PasswordHash, &g.CoverPhotoID, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get gallery by id: %w", err)
	}
	return g, nil
}

func (s *GalleryStore) ListByOwner(ctx context.Context, ownerID uuid.UUID) ([]*domain.Gallery, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT g.id, g.owner_id, g.title, g.description, g.slug, g.visibility,
		        g.password_hash, g.cover_photo_id, g.created_at, g.updated_at,
		        COUNT(p.id) AS photo_count,
		        COALESCE(
		            cp.thumb_path,
		            (SELECT p2.thumb_path FROM photos p2
		             WHERE p2.gallery_id = g.id
		             ORDER BY p2.sort_order ASC, p2.created_at ASC
		             LIMIT 1),
		            ''
		        ) AS cover_thumb
		 FROM galleries g
		 LEFT JOIN photos p ON p.gallery_id = g.id
		 LEFT JOIN photos cp ON cp.id = g.cover_photo_id
		 WHERE g.owner_id = $1
		 GROUP BY g.id, cp.thumb_path
		 ORDER BY g.created_at DESC`, ownerID,
	)
	if err != nil {
		return nil, fmt.Errorf("list galleries by owner: %w", err)
	}
	defer rows.Close()
	return scanGalleries(rows)
}

func (s *GalleryStore) ListPublic(ctx context.Context) ([]*domain.Gallery, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT g.id, g.owner_id, g.title, g.description, g.slug, g.visibility,
		        g.password_hash, g.cover_photo_id, g.created_at, g.updated_at,
		        COUNT(p.id) AS photo_count,
		        COALESCE(
		            cp.thumb_path,
		            (SELECT p2.thumb_path FROM photos p2
		             WHERE p2.gallery_id = g.id
		             ORDER BY p2.sort_order ASC, p2.created_at ASC
		             LIMIT 1),
		            ''
		        ) AS cover_thumb
		 FROM galleries g
		 LEFT JOIN photos p ON p.gallery_id = g.id
		 LEFT JOIN photos cp ON cp.id = g.cover_photo_id
		 WHERE g.visibility = 'public'
		 GROUP BY g.id, cp.thumb_path
		 ORDER BY g.updated_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list public galleries: %w", err)
	}
	defer rows.Close()
	return scanGalleries(rows)
}

func (s *GalleryStore) Update(ctx context.Context, g *domain.Gallery) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE galleries
		 SET title = $1, description = $2, slug = $3, visibility = $4,
		     password_hash = $5, updated_at = NOW()
		 WHERE id = $6`,
		g.Title, g.Description, g.Slug, g.Visibility, g.PasswordHash, g.ID,
	)
	return err
}

func (s *GalleryStore) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM galleries WHERE id = $1`, id)
	return err
}

func (s *GalleryStore) SetCover(ctx context.Context, galleryID, photoID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE galleries SET cover_photo_id = $1, updated_at = NOW() WHERE id = $2`,
		photoID, galleryID,
	)
	return err
}

func (s *GalleryStore) SlugExists(ctx context.Context, slug string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM galleries WHERE slug = $1)`, slug,
	).Scan(&exists)
	return exists, err
}

func (s *GalleryStore) AddMember(ctx context.Context, galleryID, userID uuid.UUID, role domain.MemberRole) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO gallery_members (gallery_id, user_id, role)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (gallery_id, user_id) DO UPDATE SET role = $3`,
		galleryID, userID, role,
	)
	return err
}

func (s *GalleryStore) RemoveMember(ctx context.Context, galleryID, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM gallery_members WHERE gallery_id = $1 AND user_id = $2`,
		galleryID, userID,
	)
	return err
}

func (s *GalleryStore) GetMember(ctx context.Context, galleryID, userID uuid.UUID) (*domain.GalleryMember, error) {
	m := &domain.GalleryMember{}
	err := s.pool.QueryRow(ctx,
		`SELECT gm.gallery_id, gm.user_id, gm.role, u.username, u.email
		 FROM gallery_members gm
		 JOIN users u ON u.id = gm.user_id
		 WHERE gm.gallery_id = $1 AND gm.user_id = $2`,
		galleryID, userID,
	).Scan(&m.GalleryID, &m.UserID, &m.Role, &m.Username, &m.Email)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return m, nil
}

func (s *GalleryStore) ListMembers(ctx context.Context, galleryID uuid.UUID) ([]*domain.GalleryMember, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT gm.gallery_id, gm.user_id, gm.role, u.username, u.email
		 FROM gallery_members gm
		 JOIN users u ON u.id = gm.user_id
		 WHERE gm.gallery_id = $1
		 ORDER BY u.username ASC`, galleryID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []*domain.GalleryMember
	for rows.Next() {
		m := &domain.GalleryMember{}
		if err := rows.Scan(&m.GalleryID, &m.UserID, &m.Role, &m.Username, &m.Email); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}


func scanGalleries(rows pgx.Rows) ([]*domain.Gallery, error) {
	var galleries []*domain.Gallery
	for rows.Next() {
		g := &domain.Gallery{}
		if err := rows.Scan(
			&g.ID, &g.OwnerID, &g.Title, &g.Description, &g.Slug, &g.Visibility,
			&g.PasswordHash, &g.CoverPhotoID, &g.CreatedAt, &g.UpdatedAt,
			&g.PhotoCount, &g.CoverThumb,
		); err != nil {
			return nil, err
		}
		galleries = append(galleries, g)
	}
	return galleries, rows.Err()
}
