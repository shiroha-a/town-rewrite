package content

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrImageNotFound is returned when an uploaded image name does not exist.
var ErrImageNotFound = errors.New("image not found")

// SaveImage upserts an uploaded image (admin). name is a URL slug.
func (s *Service) SaveImage(ctx context.Context, name, mime string, data []byte) error {
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO uploaded_images (name, mime, data) VALUES ($1, $2, $3)
		 ON CONFLICT (name) DO UPDATE SET mime = $2, data = $3, created_at = now()`,
		name, mime, data); err != nil {
		return fmt.Errorf("save image: %w", err)
	}
	return nil
}

// GetImage returns the mime type and bytes of an uploaded image.
func (s *Service) GetImage(ctx context.Context, name string) (string, []byte, error) {
	var mime string
	var data []byte
	err := s.pool.QueryRow(ctx,
		`SELECT mime, data FROM uploaded_images WHERE name = $1`, name).Scan(&mime, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrImageNotFound
	}
	if err != nil {
		return "", nil, fmt.Errorf("get image: %w", err)
	}
	return mime, data, nil
}

// ListImageNames returns every uploaded image name (for the asset palette).
func (s *Service) ListImageNames(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT name FROM uploaded_images ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("scan image name: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
