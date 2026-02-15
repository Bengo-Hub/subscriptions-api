package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

const tenantKey = "tenant_id"

func Tenant() gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant := strings.TrimSpace(c.GetHeader("X-Tenant-ID"))
		if tenant == "" {
			tenant = c.Param("tenantId")
		}

		if tenant != "" {
			c.Set(tenantKey, tenant)
		}

		c.Next()
	}
}

func TenantFromContext(c *gin.Context) string {
	return c.GetString(tenantKey)
}

