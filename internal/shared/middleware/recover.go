package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func Recover(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic recovered", zap.Any("error", rec), zap.String("request_id", RequestIDFromContext(c)))
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal_server_error"})
			}
		}()

		c.Next()
	}
}

