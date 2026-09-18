package httpapi

import (
	"github.com/gin-gonic/gin"
)

// ErrorResponse is the single error shape returned by every endpoint.
type ErrorResponse struct {
	Error   string            `json:"error"`
	Details map[string]string `json:"details,omitempty"`
}

func respondError(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, ErrorResponse{Error: message})
}
