package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const requestIDKey = "request_id"

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString(requestIDKey) == "" {
			c.Set(requestIDKey, uuid.NewString())
		}
		c.Writer.Header().Set("X-Request-ID", c.GetString(requestIDKey))
		c.Next()
	}
}

func RequestIDFromContext(c *gin.Context) string {
	return c.GetString(requestIDKey)
}

