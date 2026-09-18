package task

import (
	"context"
	"log/slog"
	"strings"
)

// Service is the business-logic contract the HTTP layer depends on. It sits
// between the transport and the Repository; business rules about tasks, such
// as title normalisation, belong here rather than in handlers or SQL.
type Service interface {
	List(ctx context.Context, p ListParams) ([]Task, error)
	Get(ctx context.Context, id int64) (Task, error)
	Create(ctx context.Context, p CreateParams) (Task, error)
	Update(ctx context.Context, id int64, p UpdateParams) (Task, error)
	Delete(ctx context.Context, id int64) error
}

type service struct {
	repo Repository
	log  *slog.Logger
}

// Compile-time check that the implementation satisfies the contract.
var _ Service = (*service)(nil)

// NewService returns a Service backed by the given repository. A nil logger
// falls back to slog.Default.
func NewService(repo Repository, log *slog.Logger) Service {
	if log == nil {
		log = slog.Default()
	}

	return &service{repo: repo, log: log}
}

// List returns a page of tasks.
func (s *service) List(ctx context.Context, p ListParams) ([]Task, error) {
	return s.repo.List(ctx, p)
}

// Get returns a single task by ID.
func (s *service) Get(ctx context.Context, id int64) (Task, error) {
	return s.repo.Get(ctx, id)
}

// Create normalises the input and stores a new task.
func (s *service) Create(ctx context.Context, p CreateParams) (Task, error) {
	title, err := normaliseTitle(p.Title)
	if err != nil {
		return Task{}, err
	}
	p.Title = title

	t, err := s.repo.Create(ctx, p)
	if err != nil {
		return Task{}, err
	}

	s.log.InfoContext(ctx, "task created", slog.Int64("task_id", t.ID))

	return t, nil
}

// Update normalises the input and replaces the mutable fields of a task.
func (s *service) Update(ctx context.Context, id int64, p UpdateParams) (Task, error) {
	title, err := normaliseTitle(p.Title)
	if err != nil {
		return Task{}, err
	}
	p.Title = title

	return s.repo.Update(ctx, id, p)
}

// Delete removes a task by ID.
func (s *service) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}

// normaliseTitle trims surrounding whitespace and rejects a blank title, so
// both create and update store the same shape and the database CHECK
// constraint is never the first thing to complain.
func normaliseTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", ErrInvalidTitle
	}

	return title, nil
}
