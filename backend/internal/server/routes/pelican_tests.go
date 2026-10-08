package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

func registerPelicanTestRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	pelican := admin.Group("/pelican-tests")
	pelican.GET("/options", h.Admin.PelicanTest.GetOptions)
	pelican.POST("/options", h.Admin.PelicanTest.GetAccountOptions)
	pelican.POST("/tests", h.Admin.PelicanTest.StartTests)
	pelican.GET("/tests", h.Admin.PelicanTest.ListTests)
	pelican.GET("/tests/:id", h.Admin.PelicanTest.GetTest)
}
