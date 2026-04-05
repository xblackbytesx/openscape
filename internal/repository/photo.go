package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openscape/openscape/internal/domain"
)

type PhotoStore struct {
	pool *pgxpool.Pool
}

func NewPhotoStore(pool *pgxpool.Pool) *PhotoStore {
	return &PhotoStore{pool: pool}
}

func (s *PhotoStore) Create(ctx context.Context, p *domain.Photo) (*domain.Photo, error) {
	exifJSON, _ := json.Marshal(p.ExifData)

	row := s.pool.QueryRow(ctx,
		`INSERT INTO photos (gallery_id, uploaded_by, title, description, filename,
		  storage_path, thumb_path, width, height, file_size, mime_type,
		  is_360, projection_type, exif_data, captured_at, sort_order)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		 RETURNING id, created_at`,
		p.GalleryID, p.UploadedBy, p.Title, p.Description, p.Filename,
		p.StoragePath, p.ThumbPath, p.Width, p.Height, p.FileSize, p.MimeType,
		p.Is360, p.ProjectionType, exifJSON, p.CapturedAt, p.SortOrder,
	)
	if err := row.Scan(&p.ID, &p.CreatedAt); err != nil {
		return nil, fmt.Errorf("create photo: %w", err)
	}
	return p, nil
}

func (s *PhotoStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.Photo, error) {
	p := &domain.Photo{}
	var exifJSON []byte
	err := s.pool.QueryRow(ctx,
		`SELECT id, gallery_id, uploaded_by, title, description, filename,
		        storage_path, thumb_path, width, height, file_size, mime_type,
		        is_360, projection_type, exif_data, captured_at, sort_order, created_at
		 FROM photos WHERE id = $1`, id,
	).Scan(&p.ID, &p.GalleryID, &p.UploadedBy, &p.Title, &p.Description, &p.Filename,
		&p.StoragePath, &p.ThumbPath, &p.Width, &p.Height, &p.FileSize, &p.MimeType,
		&p.Is360, &p.ProjectionType, &exifJSON, &p.CapturedAt, &p.SortOrder, &p.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get photo by id: %w", err)
	}
	if exifJSON != nil {
		_ = json.Unmarshal(exifJSON, &p.ExifData)
	}
	return p, nil
}

func (s *PhotoStore) ListByGallery(ctx context.Context, galleryID uuid.UUID) ([]*domain.Photo, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, gallery_id, uploaded_by, title, description, filename,
		        storage_path, thumb_path, width, height, file_size, mime_type,
		        is_360, projection_type, exif_data, captured_at, sort_order, created_at
		 FROM photos
		 WHERE gallery_id = $1
		 ORDER BY sort_order ASC, created_at ASC`, galleryID,
	)
	if err != nil {
		return nil, fmt.Errorf("list photos: %w", err)
	}
	defer rows.Close()

	var photos []*domain.Photo
	for rows.Next() {
		p := &domain.Photo{}
		var exifJSON []byte
		if err := rows.Scan(&p.ID, &p.GalleryID, &p.UploadedBy, &p.Title, &p.Description, &p.Filename,
			&p.StoragePath, &p.ThumbPath, &p.Width, &p.Height, &p.FileSize, &p.MimeType,
			&p.Is360, &p.ProjectionType, &exifJSON, &p.CapturedAt, &p.SortOrder, &p.CreatedAt); err != nil {
			return nil, err
		}
		if exifJSON != nil {
			_ = json.Unmarshal(exifJSON, &p.ExifData)
		}
		photos = append(photos, p)
	}
	return photos, rows.Err()
}

func (s *PhotoStore) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM photos WHERE id = $1`, id)
	return err
}

func (s *PhotoStore) Update(ctx context.Context, p *domain.Photo) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE photos SET title = $1, description = $2 WHERE id = $3`,
		p.Title, p.Description, p.ID,
	)
	return err
}

func (s *PhotoStore) Reorder(ctx context.Context, galleryID uuid.UUID, orderedIDs []uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for i, id := range orderedIDs {
		_, err := tx.Exec(ctx,
			`UPDATE photos SET sort_order = $1 WHERE id = $2 AND gallery_id = $3`,
			i, id, galleryID,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PhotoStore) GetNextSortOrder(ctx context.Context, galleryID uuid.UUID) (int, error) {
	var maxOrder int
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(sort_order), -1) + 1 FROM photos WHERE gallery_id = $1`, galleryID,
	).Scan(&maxOrder)
	return maxOrder, err
}
