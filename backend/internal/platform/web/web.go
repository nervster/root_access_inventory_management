// Package web holds small helpers shared by the HTTP handlers of every feature package.
package web

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// IDParam reads a positive numeric ID from the route, e.g. :orgId.
func IDParam(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	return id, err == nil && id > 0
}

// Fail ends the request with status and the JSON body {"error": message}.
func Fail(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": message})
}

// FailInternal logs an unexpected error and hides its details from the client (500).
func FailInternal(c *gin.Context, err error) {
	slog.Error("request failed", "method", c.Request.Method, "path", c.FullPath(), "error", err)
	Fail(c, http.StatusInternalServerError, "something went wrong")
}
