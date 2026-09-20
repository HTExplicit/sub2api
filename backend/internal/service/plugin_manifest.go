package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
)

const (
	PluginCapabilityOpenAIOAuthOutbound = "openai.oauth.outbound_transport.v1"
	PluginStateDisabled                 = "disabled"
	PluginStateStarting                 = "starting"
	PluginStateEnabled                  = "enabled"
	PluginStateError                    = "error"
	PluginStateIncompatible             = "incompatible"
	PluginSignatureTrusted              = "trusted"
	PluginSignatureUnsigned             = "unsigned"
)

var pluginIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)+$`)

var ErrPluginStateChanged = errors.New("插件状态已在其他实例中变化，请刷新后重试")

// PluginManifest 是 .s2plugin 包中可在执行二进制前检查的声明。
type PluginManifest struct {
	SourceRevision string                      `json:"source_revision,omitempty"`
	SchemaVersion  int                         `json:"schema_version"`
	ID             string                      `json:"id"`
	Name           string                      `json:"name"`
	Version        string                      `json:"version"`
	Description    string                      `json:"description,omitempty"`
	Author         string                      `json:"author,omitempty"`
	Requires       PluginRequirements          `json:"requires"`
	Capabilities   []PluginCapability          `json:"capabilities"`
	Runtimes       map[string]PluginRuntime    `json:"runtimes"`
	UI             PluginUIManifest            `json:"ui"`
	Files          map[string]string           `json:"files"`
	Dependencies   []extensionv1.Dependency    `json:"dependencies,omitempty"`
	Contributions  []extensionv1.Contribution  `json:"contributions,omitempty"`
	Operations     map[string][]string         `json:"operations,omitempty"`
	Resources      []extensionv1.ResourceGrant `json:"resources,omitempty"`
}

type PluginRequirements struct {
	Sub2API                   string   `json:"sub2api"`
	RecommendedSub2APIVersion string   `json:"recommended_sub2api_version,omitempty"`
	TestedSub2APIVersions     []string `json:"tested_sub2api_versions,omitempty"`
	PluginProtocol            int      `json:"plugin_protocol"`
	TransportAPI              int      `json:"transport_api"`
	UIBridge                  int      `json:"ui_bridge"`
	ExtensionAPI              int      `json:"extension_api,omitempty"`
}

type PluginCapability struct {
	ID          string `json:"id"`
	Platform    string `json:"platform"`
	AccountType string `json:"account_type"`
}

type PluginRuntime struct {
	Path string `json:"path"`
}

type PluginUIManifest struct {
	Entrypoint string `json:"entrypoint"`
}

type PluginSignature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id"`
	Signature string `json:"signature"`
}

// PluginCompatibility 是管理页面展示和启用门禁共同使用的兼容性结论。
type PluginCompatibility struct {
	Compatible         bool   `json:"compatible"`
	Tested             bool   `json:"tested"`
	Status             string `json:"status"`
	Message            string `json:"message"`
	CurrentSub2API     string `json:"current_sub2api_version"`
	RequiredSub2API    string `json:"required_sub2api_version"`
	RecommendedSub2API string `json:"recommended_sub2api_version"`
	PluginProtocol     int    `json:"plugin_protocol"`
	TransportAPI       int    `json:"transport_api"`
	UIBridge           int    `json:"ui_bridge"`
}

type PluginInstallation struct {
	Revision          int64               `json:"revision"`
	RuntimeGeneration int64               `json:"-"`
	PackageSHA256     string              `json:"package_sha256"`
	UpdatePolicy      string              `json:"update_policy"`
	ID                int64               `json:"id"`
	PluginKey         string              `json:"plugin_key"`
	Name              string              `json:"name"`
	Version           string              `json:"version"`
	Description       string              `json:"description"`
	Author            string              `json:"author"`
	Manifest          PluginManifest      `json:"manifest"`
	ArtifactData      []byte              `json:"-"`
	ArtifactPath      string              `json:"-"`
	InstallPath       string              `json:"-"`
	BinaryPath        string              `json:"-"`
	BinarySHA256      string              `json:"binary_sha256"`
	SignatureStatus   string              `json:"signature_status"`
	State             string              `json:"state"`
	ConfigEncrypted   string              `json:"-"`
	LastError         string              `json:"last_error"`
	InstalledBy       *int64              `json:"installed_by"`
	InstalledAt       time.Time           `json:"installed_at"`
	EnabledAt         *time.Time          `json:"enabled_at"`
	UpdatedAt         time.Time           `json:"updated_at"`
	Bindings          []PluginBinding     `json:"bindings"`
	Compatibility     PluginCompatibility `json:"compatibility"`
	RuntimeHealthy    bool                `json:"runtime_healthy"`
	RuntimeMessage    string              `json:"runtime_message"`
	DesiredEnabled    bool                `json:"desired_enabled"`
}

type PluginBinding struct {
	ID             int64     `json:"id"`
	PluginID       int64     `json:"plugin_id"`
	Capability     string    `json:"capability"`
	Platform       string    `json:"platform"`
	AccountType    string    `json:"account_type"`
	Enabled        bool      `json:"enabled"`
	RolloutPercent int       `json:"rollout_percent"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type PluginRepository interface {
	List(ctx context.Context) ([]*PluginInstallation, error)
	GetByID(ctx context.Context, id int64) (*PluginInstallation, error)
	GetByKey(ctx context.Context, key string) (*PluginInstallation, error)
	Install(ctx context.Context, plugin *PluginInstallation, bindings []PluginBinding) (*PluginInstallation, error)
	GetArtifact(ctx context.Context, id int64) ([]byte, error)
	Delete(ctx context.Context, id int64, expectedBinarySHA256 string) error
	BeginEnable(ctx context.Context, id int64, binarySHA256, expectedState string) error
	MarkRuntimeHealthy(ctx context.Context, id int64, binarySHA256, configEncrypted string) error
	UpdateState(ctx context.Context, id int64, state, lastError string, enabledAt *time.Time, expectedBinarySHA256, expectedState string) error
	UpdateConfig(ctx context.Context, id int64, encrypted, expectedBinarySHA256 string) error
	UpdateBindingsAndState(ctx context.Context, pluginID int64, bindings []PluginBinding, state, lastError string, enabledAt *time.Time, expectedState, expectedBinarySHA256 string) error
}

func (m PluginManifest) RuntimeKey() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

func (m PluginManifest) Validate() error {
	return m.ValidateForRuntime(m.RuntimeKey())
}

func (m PluginManifest) ValidateForRuntime(runtimeKey string) error {
	if m.SourceRevision != "" && !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(m.SourceRevision) {
		return errors.New("插件源码版本必须是完整提交摘要")
	}
	if m.SchemaVersion != 1 {
		return fmt.Errorf("不支持的插件清单版本: %d", m.SchemaVersion)
	}
	if !pluginIDPattern.MatchString(m.ID) || len(m.ID) > 160 {
		return errors.New("插件 ID 必须是长度不超过 160 的小写命名空间标识")
	}
	if strings.TrimSpace(m.Name) == "" || len(m.Name) > 160 {
		return errors.New("插件名称不能为空且不能超过 160 个字符")
	}
	if normalizeSemver(m.Version) == "" {
		return errors.New("插件版本必须是有效的语义化版本")
	}
	if strings.TrimSpace(m.Requires.Sub2API) == "" {
		return errors.New("插件必须声明 requires.sub2api")
	}
	if m.Requires.PluginProtocol != pluginv1.ProtocolVersion ||
		m.Requires.TransportAPI != pluginv1.TransportAPIVersion ||
		m.Requires.UIBridge != pluginv1.UIBridgeVersion {
		return errors.New("插件协议、传输 API 或 UI Bridge 版本与当前宿主不兼容")
	}
	if len(m.Capabilities) == 0 {
		return errors.New("插件必须声明至少一个能力")
	}
	for _, capability := range m.Capabilities {
		if extensionv1.IsCapability(capability.ID) {
			if m.Requires.ExtensionAPI != extensionv1.Version || capability.Platform == "" || capability.AccountType == "" {
				return errors.New("扩展能力必须声明兼容版本及明确作用范围")
			}
			if capability.ID == extensionv1.CapabilityProvider && (capability.Platform == "*" || capability.AccountType == "*") {
				return errors.New("Provider能力必须声明具体平台与账号类型")
			}
		} else if capability.ID != PluginCapabilityOpenAIOAuthOutbound || capability.Platform != PlatformOpenAI || capability.AccountType != AccountTypeOAuth {
			return fmt.Errorf("不支持插件能力 %s", capability.ID)
		}
	}
	if m.Requires.ExtensionAPI != 0 && m.Requires.ExtensionAPI != extensionv1.Version {
		return errors.New("扩展 API 版本不兼容")
	}
	for capability, operations := range m.Operations {
		declared := false
		for _, entry := range m.Capabilities {
			if entry.ID == capability {
				declared = true
				break
			}
		}
		if !declared || !extensionv1.IsCapability(capability) || len(operations) > 64 {
			return errors.New("扩展操作必须属于已声明的能力")
		}
		seenOperations := map[string]bool{}
		for _, operation := range operations {
			if len(operation) == 0 || len(operation) > 80 || !regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`).MatchString(operation) || seenOperations[operation] {
				return errors.New("扩展操作标识无效或重复")
			}
			seenOperations[operation] = true
		}
	}
	seen := make(map[string]bool)
	resourceNames := make(map[string]bool)
	if len(m.Resources) > 256 {
		return errors.New("插件资源操作数量超过限制")
	}
	for _, resource := range m.Resources {
		if !regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)+$`).MatchString(resource.Name) || len(resource.Name) > 100 || resourceNames[resource.Name] || (resource.Permission != "admin" && resource.Permission != "user") {
			return errors.New("插件资源操作声明无效")
		}
		declared := false
		for _, capability := range m.Capabilities {
			declared = declared || capability.ID == resource.Capability
		}
		if !declared {
			return errors.New("插件资源引用未声明的能力")
		}
		resourceNames[resource.Name] = true
	}
	for _, contribution := range m.Contributions {
		if len(contribution.Events) > 16 {
			return errors.New("插件界面事件数量超过限制")
		}
		for _, event := range contribution.Events {
			if !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`).MatchString(event) {
				return errors.New("插件界面事件声明无效")
			}
		}
		if contribution.Capability != "" {
			declared := false
			for _, capability := range m.Capabilities {
				if capability.ID == contribution.Capability {
					declared = true
					break
				}
			}
			if !declared {
				return errors.New("插件界面贡献引用未声明的能力")
			}
		}
		if contribution.Slot == "theme" {
			if contribution.Permission != "public" || !strings.HasSuffix(contribution.Entrypoint, ".css") || len(contribution.Assets) > 32 {
				return errors.New("主题贡献必须声明公开 CSS 和有界资源集合")
			}
		} else if len(contribution.Assets) != 0 {
			return errors.New("只有主题贡献可公开资源")
		}
		for _, asset := range contribution.Assets {
			if !safePluginRelativePath(asset) || !strings.HasPrefix(asset, "ui/") || !strings.HasSuffix(asset, ".woff2") {
				return errors.New("主题附加资源必须是包内 WOFF2 字体")
			}
		}
		if contribution.ConfigFlag != "" && !regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`).MatchString(contribution.ConfigFlag) {
			return errors.New("插件界面条件必须引用布尔配置字段")
		}
		if contribution.ID == "" || seen[contribution.ID] || !extensionv1.ValidSlot(contribution.Slot) ||
			contribution.Permission == "" || len(contribution.Label) == 0 {
			return errors.New("插件界面贡献的标识、挂载点或权限无效")
		}
		seen[contribution.ID] = true
		fieldKeys := map[string]bool{}
		if len(contribution.DisplayFields) > 16 {
			return errors.New("插件显示字段数量超限")
		}
		for _, field := range contribution.DisplayFields {
			if !regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`).MatchString(field.Key) || field.Key == "constructor" || field.Key == "prototype" || fieldKeys[field.Key] || len(field.Prefix) > 32 || len(field.Values) > 64 {
				return errors.New("插件显示字段声明无效")
			}
			if field.Kind != "text" && field.Kind != "badge" && field.Kind != "datetime" {
				return errors.New("插件显示字段类型无效")
			}
			for _, value := range field.Values {
				if len(value.Label) == 0 || (value.Tone != "" && value.Tone != "neutral" && value.Tone != "success" && value.Tone != "warning" && value.Tone != "danger" && value.Tone != "info") {
					return errors.New("插件显示值声明无效")
				}
			}
			fieldKeys[field.Key] = true
		}
		for _, field := range contribution.Fields {
			validKey := regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
			if !validKey.MatchString(field.Key) || field.Key == "constructor" || field.Key == "prototype" || fieldKeys[field.Key] || len(field.Label) == 0 || (field.DefaultSource != "" && !validKey.MatchString(field.DefaultSource)) {
				return errors.New("插件表单字段声明无效")
			}
			switch field.Kind {
			case "select":
				if !validKey.MatchString(field.OptionsSource) || len(field.DefaultLabel) == 0 {
					return errors.New("插件选择字段声明无效")
				}
			case "textarea":
				if field.Rows < 1 || field.Rows > 12 || field.MaxLength < 1 || field.MaxLength > 65536 {
					return errors.New("插件文本字段范围无效")
				}
			default:
				return errors.New("插件表单字段类型无效")
			}
			fieldKeys[field.Key] = true
		}
		if contribution.Entrypoint != "" && (!safePluginRelativePath(contribution.Entrypoint) || !strings.HasPrefix(contribution.Entrypoint, "ui/")) {
			return errors.New("插件界面贡献入口必须位于 ui/ 目录")
		}
		if contribution.Entrypoint != "" {
			if _, declared := m.Files[contribution.Entrypoint]; !declared {
				return errors.New("插件界面贡献入口未包含在签名资源中")
			}
		}
	}
	for _, dependency := range m.Dependencies {
		if !extensionv1.IsCapability(dependency.Capability) && dependency.Capability != PluginCapabilityOpenAIOAuthOutbound {
			return errors.New("插件依赖能力未知")
		}
	}
	runtimeEntry, ok := m.Runtimes[runtimeKey]
	if !ok || !safePluginRelativePath(runtimeEntry.Path) {
		return fmt.Errorf("插件不支持当前运行平台 %s", runtimeKey)
	}
	if !safePluginRelativePath(m.UI.Entrypoint) || !strings.HasPrefix(m.UI.Entrypoint, "ui/") {
		return errors.New("插件 UI 入口必须位于 ui/ 目录")
	}
	if len(m.Files) == 0 {
		return errors.New("插件清单必须声明文件哈希")
	}
	for path, hash := range m.Files {
		if !safePluginRelativePath(path) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(hash) {
			return fmt.Errorf("插件文件声明无效: %s", path)
		}
	}
	if _, ok := m.Files[runtimeEntry.Path]; !ok {
		return errors.New("运行时二进制未包含在文件哈希声明中")
	}
	if _, ok := m.Files[m.UI.Entrypoint]; !ok {
		return errors.New("UI 入口未包含在文件哈希声明中")
	}
	return nil
}

func safePluginRelativePath(path string) bool {
	cleaned := strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
	return cleaned != "" && cleaned != "." && !strings.HasPrefix(cleaned, "/") &&
		!strings.HasPrefix(cleaned, "../") && !strings.Contains(cleaned, "/../") && cleaned == strings.TrimPrefix(cleaned, "./")
}

func (m PluginManifest) MarshalJSONBytes() ([]byte, error) {
	return json.Marshal(m)
}

func (m PluginManifest) SortedCapabilities() []PluginCapability {
	out := append([]PluginCapability(nil), m.Capabilities...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
