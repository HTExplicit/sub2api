import { mountPlugin } from '@sub2api/plugin-ui'
import '@sub2api/plugin-ui/base.css'
import App from './App.vue'
import zh from './locales/zh'
import en from './locales/en'

void mountPlugin(App, {
  zh: { admin: zh, common: { confirm: '确认', cancel: '取消', close: '关闭', loading: '加载中', error: '操作失败' } },
  en: { admin: en, common: { confirm: 'Confirm', cancel: 'Cancel', close: 'Close', loading: 'Loading', error: 'Operation failed' } }
}).catch(error => { document.getElementById('app')!.textContent = error instanceof Error ? error.message : 'Plugin unavailable' })
