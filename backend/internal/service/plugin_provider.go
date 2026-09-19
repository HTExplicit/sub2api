package service

import "github.com/Wei-Shaw/sub2api/internal/config"

func ProvidePluginManager(repo PluginRepository, encryptor SecretEncryptor, cfg *config.Config, hostInfo PluginHostInfo, kv PluginKVStore, activity *QuotaActivityService, gateway *OpenAIGatewayService) *PluginManager {
	manager := NewPluginManager(repo, encryptor, cfg, hostInfo, kv)
	manager.quotaActivity = activity
	manager.SetAccountDirectory(gateway)
	processExtensionCatalog.Store(&extensionCatalogProvider{resolver: manager})
	return manager
}
