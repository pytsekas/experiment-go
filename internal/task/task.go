// Package task contains the example domain resource served by the API: its
// model, its business rules (Service), its storage contract (Repository) and
// the Postgres implementation of that contract.
package task

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a task does not exist.
var ErrNotFound = errors.New("task not found")

// ErrInvalidTitle is returned when a task title is empty after trimming.
var ErrInvalidTitle = errors.New("task title must not be blank")

// Task is the domain model exposed by the API.
type Task struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Done      bool      `json:"done"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateParams carries the fields needed to create a task.
type CreateParams struct {
	Title string
}

// UpdateParams carries the fields needed to update a task.
type UpdateParams struct {
	Title string
	Done  bool
}

// ListParams controls pagination when listing tasks.
type ListParams struct {
	Limit  int
	Offset int
}

// Repository is the storage contract the HTTP layer depends on. Keeping it an
// interface lets handlers be tested without a database.
type Repository interface {
	List(ctx context.Context, p ListParams) ([]Task, error)
	Get(ctx context.Context, id int64) (Task, error)
	Create(ctx context.Context, p CreateParams) (Task, error)
	Update(ctx context.Context, id int64, p UpdateParams) (Task, error)
	Delete(ctx context.Context, id int64) error
}
