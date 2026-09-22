import { mountPlugin } from '@sub2api/plugin-ui'
import '@sub2api/plugin-ui/base.css'
import App from './App.vue'
import zh from './locales/zh'
import en from './locales/en'
void mountPlugin(App, { zh, en }).catch(error => { document.getElementById('app')!.textContent = error instanceof Error ? error.message : 'Plugin unavailable' })
