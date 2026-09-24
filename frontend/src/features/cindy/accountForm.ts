import type { AccountCreateDefinitionV1 } from '@/types/accountCreate'
import type { AccountEditDefinitionV1 } from '@/types/accountEdit'

export interface CindyCreateChoice {
  label: Record<string, string>
  account_create: AccountCreateDefinitionV1
  fields: Array<{ key: string; kind: string; label: Record<string, string>; max_length: number; hint?: Record<string, string> }>
}
// Native Cindy controls are shipped with the application, not read from an
// installation, contribution registry or iframe.
export const cindyCreateChoice: CindyCreateChoice = {
  "label": {
    "zh": "Cindy",
    "en": "Cindy"
  },
  "account_create": {
    "version": 1,
    "platform": "cindy",
    "account_type": "apikey",
    "credential_profile": "cindy_laxa_v1",
    "credential_ui": {
      "base_url": "https://api.laxarouter.ai",
      "base_url_readonly": true,
      "api_key_placeholder": "cindy-...",
      "hint": {
        "zh": "使用 Cindy API Key",
        "en": "Use a Cindy API key"
      },
      "base_url_hint": {
        "zh": "Cindy 使用声明的固定服务地址。",
        "en": "Cindy uses the fixed endpoint declared by its provider."
      },
      "account_type_hint": {
        "zh": "以 Cindy API Key 创建账号。未选择分组时由服务端尝试默认分组，最终必须有有效分组。",
        "en": "Create a Cindy API-key account. When no group is selected, the server tries its default group; the final account must have an effective group."
      },
      "catalog_label": {
        "zh": "供应商管理的模型目录",
        "en": "Provider-managed model catalog"
      },
      "catalog_hint": {
        "zh": "可用模型由供应商目录提供，创建时不编辑映射。",
        "en": "Model IDs come from the provider catalog; mappings are not edited during creation."
      }
    },
    "defaults": {
      "concurrency": 3,
      "priority": 50,
      "rate_multiplier": 1,
      "load_factor": null,
      "responses_mode": "force_responses"
    },
    "minimum_effective_groups": 1,
    "upstream_billing_probe": "unsupported",
    "model_editing": "provider_managed",
    "catalog_source": "group_model_candidates",
    "field_bindings": {
      "device_id": "provider_device_identity_input"
    }
  },
  "fields": [
    {
      "key": "device_id",
      "kind": "text",
      "label": {
        "zh": "设备 ID",
        "en": "Device ID"
      },
      "max_length": 64,
      "hint": {
        "zh": "留空由服务端生成；填写的设备身份由服务端验证并保留。",
        "en": "Leave empty for server generation. The server validates and preserves a supplied device identity."
      }
    }
  ]
}
export const cindyEditDefinition: AccountEditDefinitionV1 = {
  "version": 1,
  "platform": "cindy",
  "account_type": "apikey",
  "credential_profile": "cindy_laxa_v1",
  "credential_ui": {
    "base_url": "https://api.laxarouter.ai",
    "base_url_readonly": true,
    "hint": {
      "zh": "凭据和设备身份由宿主管理。",
      "en": "The host manages credentials and device identity."
    }
  },
  "catalog_source": "provider_catalog_snapshot_v1",
  "mapping_policy": "managed_catalog_v1",
  "compact_mapping_editable": true,
  "wire_controls": [
    {
      "target": "responses_mode",
      "values": [
        "auto",
        "force_responses",
        "force_chat_completions"
      ],
      "allow_clear": true
    },
    {
      "target": "compact_mode",
      "values": [
        "auto",
        "force_on",
        "force_off"
      ],
      "allow_clear": true
    },
    {
      "target": "responses_websocket_mode",
      "values": [
        "off",
        "ctx_pool",
        "passthrough",
        "http_bridge"
      ],
      "allow_clear": true
    }
  ],
  "unlisted_fields": "preserve",
  "labels": {
    "managed_catalog": {
      "zh": "托管目录",
      "en": "Managed catalog"
    },
    "managed_aliases": {
      "zh": "托管别名",
      "en": "Managed aliases"
    },
    "custom_mappings": {
      "zh": "自定义映射",
      "en": "Custom mappings"
    }
  }
}
