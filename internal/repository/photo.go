package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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

// Create inserts a photo row. If p.ID is the zero UUID the database assigns
// one (gen_random_uuid default); otherwise the supplied ID is used. The
// uploader code paths set ID to match the on-disk filename so filename and
// row stay aligned.
func (s *PhotoStore) Create(ctx context.Context, p *domain.Photo) (*domain.Photo, error) {
	exifJSON, _ := json.Marshal(p.ExifData)

	if p.Status == "" {
		p.Status = domain.PhotoStatusReady
	}

	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}

	row := s.pool.QueryRow(ctx,
		`INSERT INTO photos (id, gallery_id, uploaded_by, title, description, filename,
		  storage_path, thumb_path, width, height, file_size, mime_type,
		  is_360, projection_type, exif_data, captured_at, duration, sort_order,
		  status, processing_error)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
		 RETURNING created_at`,
		p.ID, p.GalleryID, p.UploadedBy, p.Title, p.Description, p.Filename,
		p.StoragePath, p.ThumbPath, p.Width, p.Height, p.FileSize, p.MimeType,
		p.Is360, p.ProjectionType, exifJSON, p.CapturedAt, p.Duration, p.SortOrder,
		string(p.Status), p.ProcessingError,
	)
	if err := row.Scan(&p.CreatedAt); err != nil {
		return nil, fmt.Errorf("create photo: %w", err)
	}
	return p, nil
}

const photoSelectColumns = `id, gallery_id, uploaded_by, title, description, filename,
	        storage_path, thumb_path, width, height, file_size, mime_type,
	        is_360, projection_type, exif_data, captured_at, duration, sort_order,
	        status, processing_error, created_at`

func scanPhoto(row pgx.Row, p *domain.Photo) error {
	var exifJSON []byte
	var status string
	if err := row.Scan(&p.ID, &p.GalleryID, &p.UploadedBy, &p.Title, &p.Description, &p.Filename,
		&p.StoragePath, &p.ThumbPath, &p.Width, &p.Height, &p.FileSize, &p.MimeType,
		&p.Is360, &p.ProjectionType, &exifJSON, &p.CapturedAt, &p.Duration, &p.SortOrder,
		&status, &p.ProcessingError, &p.CreatedAt); err != nil {
		return err
	}
	p.Status = domain.PhotoStatus(status)
	if exifJSON != nil {
		_ = json.Unmarshal(exifJSON, &p.ExifData)
	}
	return nil
}

func (s *PhotoStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.Photo, error) {
	p := &domain.Photo{}
	row := s.pool.QueryRow(ctx, `SELECT `+photoSelectColumns+` FROM photos WHERE id = $1`, id)
	if err := scanPhoto(row, p); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get photo by id: %w", err)
	}
	return p, nil
}

// MaxPhotoListLimit caps how many photos a single gallery view loads at once.
// Real pagination (HTMX infinite scroll) is a follow-up; this bound is a hard
// safety net so a 50K-photo gallery doesn't OOM the server / browser.
const MaxPhotoListLimit = 500

func (s *PhotoStore) ListByGallery(ctx context.Context, galleryID uuid.UUID) ([]*domain.Photo, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+photoSelectColumns+`
		 FROM photos
		 WHERE gallery_id = $1
		 ORDER BY sort_order ASC, created_at ASC
		 LIMIT $2`, galleryID, MaxPhotoListLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("list photos: %w", err)
	}
	defer rows.Close()

	var photos []*domain.Photo
	for rows.Next() {
		p := &domain.Photo{}
		if err := scanPhoto(rows, p); err != nil {
			return nil, err
		}
		photos = append(photos, p)
	}
	return photos, rows.Err()
}

// Neighbors returns the IDs of the photos immediately before and after the
// given photo in display order. Either may be nil at the boundaries. This
// replaces the old "load every photo and loop in Go" approach used by the
// photo viewer's prev/next links.
func (s *PhotoStore) Neighbors(ctx context.Context, galleryID, photoID uuid.UUID) (prev, next *uuid.UUID, err error) {
	// Read the current photo's sort key once so the neighbor queries are
	// strictly ordered by (sort_order, created_at, id).
	var curOrder int
	var curCreated time.Time
	err = s.pool.QueryRow(ctx,
		`SELECT sort_order, created_at FROM photos WHERE id = $1 AND gallery_id = $2`,
		photoID, galleryID,
	).Scan(&curOrder, &curCreated)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}

	const prevQuery = `SELECT id FROM photos
		 WHERE gallery_id = $1
		   AND (sort_order, created_at, id) < ($2, $3, $4)
		 ORDER BY sort_order DESC, created_at DESC, id DESC
		 LIMIT 1`
	const nextQuery = `SELECT id FROM photos
		 WHERE gallery_id = $1
		   AND (sort_order, created_at, id) > ($2, $3, $4)
		 ORDER BY sort_order ASC, created_at ASC, id ASC
		 LIMIT 1`

	var pid uuid.UUID
	if e := s.pool.QueryRow(ctx, prevQuery, galleryID, curOrder, curCreated, photoID).Scan(&pid); e == nil {
		prev = &pid
	} else if e != pgx.ErrNoRows {
		return nil, nil, e
	}

	var nid uuid.UUID
	if e := s.pool.QueryRow(ctx, nextQuery, galleryID, curOrder, curCreated, photoID).Scan(&nid); e == nil {
		next = &nid
	} else if e != pgx.ErrNoRows {
		return nil, nil, e
	}
	return prev, next, nil
}

// UpdateProcessed flips a photo from 'processing' to 'ready' and updates the
// fields that the background worker computed (thumbnail, dimensions, captured-at,
// duration, projection). Used by internal/worker.
func (s *PhotoStore) UpdateProcessed(ctx context.Context, p *domain.Photo) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE photos
		 SET thumb_path = $1, width = $2, height = $3, captured_at = $4,
		     duration = $5, projection_type = $6, is_360 = $7,
		     status = 'ready', processing_error = ''
		 WHERE id = $8`,
		p.ThumbPath, p.Width, p.Height, p.CapturedAt, p.Duration,
		p.ProjectionType, p.Is360, p.ID,
	)
	return err
}

// MarkFailed flips a photo to 'failed' with a reason. The thumbnail (placeholder
// or whatever was last written) stays in place so the UI still has something to
// show.
func (s *PhotoStore) MarkFailed(ctx context.Context, photoID uuid.UUID, reason string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE photos SET status = 'failed', processing_error = $1 WHERE id = $2`,
		reason, photoID,
	)
	return err
}

func (s *PhotoStore) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM photos WHERE id = $1`, id)
	return err
}

func (s *PhotoStore) UpdateCapturedAt(ctx context.Context, photoID uuid.UUID, t time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE photos SET captured_at = $1 WHERE id = $2`,
		t, photoID,
	)
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

// SortByDate reorders all photos in the gallery chronologically, rewriting sort_order values.
// desc=true puts the most recent photo first (sort_order 0).
func (s *PhotoStore) SortByDate(ctx context.Context, galleryID uuid.UUID, desc bool) error {
	const queryDesc = `SELECT id FROM photos WHERE gallery_id = $1
		 ORDER BY COALESCE(captured_at, created_at) DESC`
	const queryAsc = `SELECT id FROM photos WHERE gallery_id = $1
		 ORDER BY COALESCE(captured_at, created_at) ASC`
	q := queryAsc
	if desc {
		q = queryDesc
	}
	rows, err := s.pool.Query(ctx, q, galleryID)
	if err != nil {
		return fmt.Errorf("sort by date query: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return s.Reorder(ctx, galleryID, ids)
}
