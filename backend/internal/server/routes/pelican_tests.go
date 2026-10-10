package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

func registerPelicanTestRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	pelican := admin.Group("/pelican-tests")
	pelican.GET("/options", h.Admin.PelicanTest.GetOptions)
	pelican.POST("/options", h.Admin.PelicanTest.GetAccountOptions)
	pelican.POST("/tasks", h.Admin.PelicanTest.StartTask)
	pelican.GET("/tasks", h.Admin.PelicanTest.ListTests)
	pelican.GET("/tasks/:id", h.Admin.PelicanTest.GetTask)
	pelican.GET("/tasks/:id/results", h.Admin.PelicanTest.GetTaskResults)
	pelican.GET("/tasks/:id/results/:result_id", h.Admin.PelicanTest.GetTaskResult)
	pelican.GET("/tasks/:id/events", h.Admin.PelicanTest.ObserveTask)
	pelican.POST("/tasks/:id/cancel", h.Admin.PelicanTest.CancelTask)
	pelican.POST("/tests", h.Admin.PelicanTest.StartTests)
	pelican.GET("/tests", h.Admin.PelicanTest.ListTests)
	pelican.GET("/tests/:id", h.Admin.PelicanTest.GetTest)
}
