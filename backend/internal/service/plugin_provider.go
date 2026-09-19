package service

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func ProvidePluginManager(repo PluginRepository, encryptor SecretEncryptor, cfg *config.Config, hostInfo PluginHostInfo, kv PluginKVStore, activity *QuotaActivityService, gateway *OpenAIGatewayService, traffic AccountTrafficObserveCache) *PluginManager {
	manager := NewPluginManager(repo, encryptor, cfg, hostInfo, kv)
	manager.quotaActivity = activity
	manager.traffic = traffic
	manager.SetAccountDirectory(gateway)
	ConfigureProcessExtensionServices(manager, manager)
	return manager
}

// ConfigureProcessExtensionServices is application composition, called before
// serving requests. Production supplies the plugin manager; contract fixtures
// can use the same independent modules without spawning one process per case.
func ConfigureProcessExtensionServices(catalog extensionv1.CatalogResolver, operations extensionv1.OperationInvoker) {
	if catalog == nil {
		processExtensionCatalog.Store(nil)
	} else {
		processExtensionCatalog.Store(&extensionCatalogProvider{resolver: catalog})
	}
	if operations == nil {
		processExtensionOperations.Store(nil)
	} else {
		processExtensionOperations.Store(&extensionOperationProvider{invoker: operations})
	}
}
