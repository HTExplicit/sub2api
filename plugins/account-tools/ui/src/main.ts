import { mountPlugin } from '@sub2api/plugin-ui'
import '@sub2api/plugin-ui/base.css'
import App from './App.vue'
import zhAccounts from './locales/zh-accounts'
import enAccounts from './locales/en-accounts'
import zhCommon from './locales/zh-common'
import enCommon from './locales/en-common'

void mountPlugin(App, {
  zh: { ...zhCommon, admin: zhAccounts, tools: { reasoningLabel: '推理强度', reasoningDefault: '默认（沿用模型默认行为）' } },
  en: { ...enCommon, admin: enAccounts, tools: { reasoningLabel: 'Reasoning effort', reasoningDefault: 'Default (model default)' } }
}).catch(error => { document.getElementById('app')!.textContent = error instanceof Error ? error.message : 'Plugin unavailable' })
