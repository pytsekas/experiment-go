package task

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresRepository stores tasks in Postgres.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresRepository returns a Repository backed by the given pool.
func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// Compile-time check that the implementation satisfies the contract.
var _ Repository = (*PostgresRepository)(nil)

const columns = `id, title, done, created_at, updated_at`

// List returns a page of tasks, newest first.
func (r *PostgresRepository) List(ctx context.Context, p ListParams) ([]Task, error) {
	if p.Limit <= 0 || p.Limit > 100 {
		p.Limit = 20
	}
	if p.Offset < 0 {
		p.Offset = 0
	}

	rows, err := r.pool.Query(ctx,
		`SELECT `+columns+` FROM tasks ORDER BY id DESC LIMIT $1 OFFSET $2`,
		p.Limit, p.Offset)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	tasks := make([]Task, 0, p.Limit)
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Done, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tasks: %w", err)
	}

	return tasks, nil
}

// Get returns a single task by ID.
func (r *PostgresRepository) Get(ctx context.Context, id int64) (Task, error) {
	var t Task
	err := r.pool.QueryRow(ctx, `SELECT `+columns+` FROM tasks WHERE id = $1`, id).
		Scan(&t.ID, &t.Title, &t.Done, &t.CreatedAt, &t.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Task{}, ErrNotFound
	case err != nil:
		return Task{}, fmt.Errorf("get task %d: %w", id, err)
	}

	return t, nil
}

// Create inserts a task and returns the stored row.
func (r *PostgresRepository) Create(ctx context.Context, p CreateParams) (Task, error) {
	var t Task
	err := r.pool.QueryRow(ctx,
		`INSERT INTO tasks (title) VALUES ($1) RETURNING `+columns, p.Title).
		Scan(&t.ID, &t.Title, &t.Done, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return Task{}, fmt.Errorf("create task: %w", err)
	}

	return t, nil
}

// Update replaces the mutable fields of a task and returns the stored row.
func (r *PostgresRepository) Update(ctx context.Context, id int64, p UpdateParams) (Task, error) {
	var t Task
	err := r.pool.QueryRow(ctx,
		`UPDATE tasks SET title = $1, done = $2, updated_at = now() WHERE id = $3 RETURNING `+columns,
		p.Title, p.Done, id).
		Scan(&t.ID, &t.Title, &t.Done, &t.CreatedAt, &t.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Task{}, ErrNotFound
	case err != nil:
		return Task{}, fmt.Errorf("update task %d: %w", id, err)
	}

	return t, nil
}

// Delete removes a task by ID.
func (r *PostgresRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM tasks WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete task %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}
