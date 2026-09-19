package service

import (
	"context"
	"github.com/gin-gonic/gin"
)

func promptPolicyRequestContext(c *gin.Context) context.Context {
	if c != nil && c.Request != nil {
		return c.Request.Context()
	}
	return context.Background()
}
