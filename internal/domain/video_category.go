package domain

import (
	"time"

	"github.com/google/uuid"
)

type VideoCategory struct {
	ID           uuid.UUID  `json:"id" db:"id"`
	Name         string     `json:"name" db:"name"`
	Slug         string     `json:"slug" db:"slug"`
	Description  *string    `json:"description,omitempty" db:"description"`
	Icon         *string    `json:"icon,omitempty" db:"icon"`
	Color        *string    `json:"color,omitempty" db:"color"`
	DisplayOrder int        `json:"display_order" db:"display_order"`
	IsActive     bool       `json:"is_active" db:"is_active"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
	CreatedBy    *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
}

type CreateVideoCategoryRequest struct {
	Name         string  `json:"name" validate:"required,min=1,max=100"`
	Slug         string  `json:"slug" validate:"required,min=1,max=100,slug"`
	Description  *string `json:"description,omitempty" validate:"omitempty,max=500"`
	Icon         *string `json:"icon,omitempty" validate:"omitempty,max=50"`
	Color        *string `json:"color,omitempty" validate:"omitempty,hexcolor"`
	DisplayOrder int     `json:"display_order" validate:"min=0"`
	IsActive     bool    `json:"is_active"`
}

type UpdateVideoCategoryRequest struct {
	Name         *string `json:"name,omitempty" validate:"omitempty,min=1,max=100"`
	Slug         *string `json:"slug,omitempty" validate:"omitempty,min=1,max=100,slug"`
	Description  *string `json:"description,omitempty" validate:"omitempty,max=500"`
	Icon         *string `json:"icon,omitempty" validate:"omitempty,max=50"`
	Color        *string `json:"color,omitempty" validate:"omitempty,hexcolor"`
	DisplayOrder *int    `json:"display_order,omitempty" validate:"omitempty,min=0"`
	IsActive     *bool   `json:"is_active,omitempty"`
}

type VideoCategoryListOptions struct {
	ActiveOnly bool   `json:"active_only"`
	OrderBy    string `json:"order_by" validate:"omitempty,oneof=name slug display_order created_at"`
	OrderDir   string `json:"order_dir" validate:"omitempty,oneof=asc desc"`
	Limit      int    `json:"limit" validate:"omitempty,min=1,max=100"`
	Offset     int    `json:"offset" validate:"omitempty,min=0"`
}
