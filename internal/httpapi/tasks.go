package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/pytsekas/experiment-go/internal/task"
)

// taskHandler exposes the tasks resource over HTTP.
type taskHandler struct {
	svc task.Service
	log *slog.Logger
}

type createTaskRequest struct {
	Title string `json:"title" binding:"required,min=1,max=200"`
}

type updateTaskRequest struct {
	Title string `json:"title" binding:"required,min=1,max=200"`
	Done  bool   `json:"done"`
}

type listTasksQuery struct {
	Limit  int `form:"limit" binding:"omitempty,min=1,max=100"`
	Offset int `form:"offset" binding:"omitempty,min=0"`
}

// list handles GET /tasks.
func (h taskHandler) list(c *gin.Context) {
	var q listTasksQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		respondError(c, http.StatusBadRequest, "invalid query parameters: "+err.Error())

		return
	}

	tasks, err := h.svc.List(c.Request.Context(), task.ListParams{Limit: q.Limit, Offset: q.Offset})
	if err != nil {
		h.fail(c, err, "list tasks")

		return
	}

	c.JSON(http.StatusOK, gin.H{"data": tasks})
}

// get handles GET /tasks/:id.
func (h taskHandler) get(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	t, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		h.fail(c, err, "get task")

		return
	}

	c.JSON(http.StatusOK, gin.H{"data": t})
}

// create handles POST /tasks.
func (h taskHandler) create(c *gin.Context) {
	var req createTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body: "+err.Error())

		return
	}

	t, err := h.svc.Create(c.Request.Context(), task.CreateParams{Title: req.Title})
	if err != nil {
		h.fail(c, err, "create task")

		return
	}

	c.Header("Location", "/api/v1/tasks/"+strconv.FormatInt(t.ID, 10))
	c.JSON(http.StatusCreated, gin.H{"data": t})
}

// update handles PUT /tasks/:id.
func (h taskHandler) update(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	var req updateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body: "+err.Error())

		return
	}

	t, err := h.svc.Update(c.Request.Context(), id, task.UpdateParams{Title: req.Title, Done: req.Done})
	if err != nil {
		h.fail(c, err, "update task")

		return
	}

	c.JSON(http.StatusOK, gin.H{"data": t})
}

// remove handles DELETE /tasks/:id.
func (h taskHandler) remove(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		h.fail(c, err, "delete task")

		return
	}

	c.Status(http.StatusNoContent)
}

// fail maps a service error onto an HTTP response, logging unexpected ones.
func (h taskHandler) fail(c *gin.Context, err error, op string) {
	switch {
	case errors.Is(err, task.ErrNotFound):
		respondError(c, http.StatusNotFound, "task not found")

		return
	case errors.Is(err, task.ErrInvalidTitle):
		respondError(c, http.StatusBadRequest, task.ErrInvalidTitle.Error())

		return
	}

	h.log.ErrorContext(c.Request.Context(), "request error",
		slog.String("op", op),
		slog.Any("error", err),
		slog.String(requestIDKey, c.GetString(requestIDKey)),
	)
	respondError(c, http.StatusInternalServerError, "internal server error")
}

func parseID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		respondError(c, http.StatusBadRequest, "id must be a positive integer")

		return 0, false
	}

	return id, true
}
