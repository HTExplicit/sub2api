package service

import "github.com/Wei-Shaw/sub2api/internal/config"

// ProvidePluginManager composes the upstream third-party framework only.
func ProvidePluginManager(repo PluginRepository, encryptor SecretEncryptor, cfg *config.Config, hostInfo PluginHostInfo, kv PluginKVStore, _ *QuotaActivityService, gateway *OpenAIGatewayService, _ AccountTrafficObserveCache) *PluginManager {
	manager := NewPluginManager(repo, encryptor, cfg, hostInfo, kv)
	manager.SetAccountDirectory(gateway)
	return manager
}
