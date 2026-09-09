package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

func registerAccountCapabilityRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	capabilities := admin.Group("/account-capabilities")
	if h.Admin.AccountCapability != nil {
		jobs := h.Admin.AccountCapability
		capabilities.GET("", jobs.Inventory)
		capabilities.GET("/runs", jobs.ListRuns)
		capabilities.POST("/runs", jobs.CreateRun)
		capabilities.GET("/runs/receipt", jobs.GetRunReceipt)
		capabilities.GET("/runs/:id", jobs.GetRun)
		capabilities.GET("/runs/:id/items", jobs.ListItems)
		capabilities.POST("/runs/:id/pause", jobs.Pause)
		capabilities.POST("/runs/:id/resume", jobs.Resume)
		capabilities.POST("/runs/:id/cancel", jobs.Cancel)
	}
	if h.Admin.AccountCapabilityCatalog != nil {
		capabilities.GET("/candidates", h.Admin.AccountCapabilityCatalog.Candidates)
		capabilities.GET("/overview", h.Admin.AccountCapabilityCatalog.Overview)
		capabilities.POST("/plan", h.Admin.AccountCapabilityCatalog.Plan)
	}
	if h.Admin.AccountCapabilityPublication != nil {
		publication := h.Admin.AccountCapabilityPublication
		capabilities.POST("/changesets/preview", publication.Preview)
		capabilities.GET("/changesets/:id", publication.Get)
		capabilities.POST("/changesets/:id/apply", publication.Apply)
	}
}
