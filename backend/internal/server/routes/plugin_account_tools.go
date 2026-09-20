package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/gin-gonic/gin"
)

func registerAccountToolResources(accounts *gin.RouterGroup, h *handler.Handlers) {
	for _, route := range []struct {
		name, method, path, accountParam, accountBody, accountItems, filter string
		handler                                                             gin.HandlerFunc
	}{
		{"taxonomy.folders.list", "GET", "/folders", "", "", "", "", h.Admin.Account.ListAccountFolders},
		{"taxonomy.folders.create", "POST", "/folders", "", "", "", "", h.Admin.Account.CreateAccountFolder},
		{"taxonomy.folders.order", "PUT", "/folders/order", "", "", "", "", h.Admin.Account.ReorderAccountFolders},
		{"taxonomy.folders.update", "PUT", "/folders/:id", "", "", "", "", h.Admin.Account.UpdateAccountFolder},
		{"taxonomy.folders.delete", "DELETE", "/folders/:id", "", "", "", "", h.Admin.Account.DeleteAccountFolder},
		{"taxonomy.tags.list", "GET", "/tags", "", "", "", "", h.Admin.Account.ListAccountTags},
		{"taxonomy.tags.create", "POST", "/tags", "", "", "", "", h.Admin.Account.CreateAccountTag},
		{"taxonomy.tags.order", "PUT", "/tags/order", "", "", "", "", h.Admin.Account.ReorderAccountTags},
		{"taxonomy.tags.update", "PUT", "/tags/:id", "", "", "", "", h.Admin.Account.UpdateAccountTag},
		{"taxonomy.tags.delete", "DELETE", "/tags/:id", "", "", "", "", h.Admin.Account.DeleteAccountTag},
		{"taxonomy.account.update", "PUT", "/:id/taxonomy", "id", "", "", "", h.Admin.Account.SetAccountTaxonomy},
		{"taxonomy.bulk.update", "POST", "/bulk-taxonomy", "", "account_ids", "", "filters", h.Admin.Account.BulkUpdateAccountTaxonomy},
		{"tests.models", "POST", "/batch-test-models", "", "account_ids", "", "", h.Admin.Account.BatchTestModels},
		{"tests.submit", "POST", "/batch-test", "", "account_ids", "items", "", h.Admin.Account.BatchTest},
	} {
		descriptor := extensionv1.ResourceDescriptor{ResourceGrant: extensionv1.ResourceGrant{Name: route.name, Capability: extensionv1.CapabilityAdmin, Permission: "admin"}, Method: route.method, Path: accounts.BasePath() + route.path,
			AccountParam: route.accountParam, AccountBodyField: route.accountBody, AccountItemsField: route.accountItems, FilterField: route.filter}
		accounts.Handle(route.method, route.path, h.Admin.Plugin.RegisterResource(descriptor), route.handler)
	}
}
