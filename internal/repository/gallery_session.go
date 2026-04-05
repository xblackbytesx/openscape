package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openscape/openscape/internal/domain"
)

type GallerySessionStore struct {
	pool *pgxpool.Pool
}

func NewGallerySessionStore(pool *pgxpool.Pool) *GallerySessionStore {
	return &GallerySessionStore{pool: pool}
}

func (s *GallerySessionStore) Create(ctx context.Context, gs *domain.GallerySession) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO gallery_sessions (token, gallery_id, expires_at)
		 VALUES ($1, $2, $3)`,
		gs.Token, gs.GalleryID, gs.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("create gallery session: %w", err)
	}
	return nil
}


func (s *GallerySessionStore) GetByGallery(ctx context.Context, token string, galleryID uuid.UUID) (*domain.GallerySession, error) {
	gs := &domain.GallerySession{}
	err := s.pool.QueryRow(ctx,
		`SELECT token, gallery_id, created_at, expires_at
		 FROM gallery_sessions
		 WHERE token = $1 AND gallery_id = $2 AND expires_at > NOW()`, token, galleryID,
	).Scan(&gs.Token, &gs.GalleryID, &gs.CreatedAt, &gs.ExpiresAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return gs, nil
}

func (s *GallerySessionStore) DeleteExpired(ctx context.Context) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM gallery_sessions WHERE expires_at < $1`, time.Now(),
	)
	return err
}
