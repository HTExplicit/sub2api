// Builds only plugin-owned source and the public SDK. The existing frozen
// frontend lock supplies build dependencies, never private host components.
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { createRequire } from 'node:module'
import preset from './tailwind.preset.mjs'

const sdk = path.dirname(fileURLToPath(import.meta.url))
const root = path.resolve(sdk, '../../../..')
const require = createRequire(path.join(root, 'frontend/package.json'))
const { build } = require('vite')
const vue = require('@vitejs/plugin-vue')
const tailwind = require('tailwindcss')
const autoprefixer = require('autoprefixer')
const inventory = JSON.parse(fs.readFileSync(path.join(root, 'plugins/bundle.source.json'), 'utf8'))
const selected = process.argv[2]
if (selected && !inventory.plugins.some(item => item.directory === selected)) throw new Error('Unknown plugin domain')
for (const { directory } of inventory.plugins) {
  if (selected && selected !== directory) continue
  if (!/^[a-z][a-z0-9-]*$/.test(directory)) throw new Error('Invalid plugin directory')
  const ui = path.join(root, 'plugins', directory, 'ui')
  const source = path.join(ui, 'src')
  if (!fs.existsSync(path.join(source, 'index.html'))) continue
  await build({
    root: source, configFile: false, base: './',
    plugins: [vue(), {
      name: 'public-plugin-source-boundary',
      resolveId(id) { if (id.startsWith('@/')) throw new Error('Plugins cannot import private host frontend modules') },
      load(id) {
        const file = id.replaceAll('\\', '/')
        if (file.startsWith(root.replaceAll('\\', '/') + '/frontend/src/') || file.startsWith(root.replaceAll('\\', '/') + '/backend/internal/')) {
          throw new Error('Plugins may import only their own source and the public SDK')
        }
      }
    }],
    resolve: {
      dedupe: ['vue', 'vue-i18n'],
      alias: {
        '@sub2api/plugin-ui': sdk,
        vue: path.join(root, 'frontend/node_modules/vue/dist/vue.runtime.esm-bundler.js'),
        'vue-i18n': path.join(root, 'frontend/node_modules/vue-i18n/dist/vue-i18n.runtime.esm-bundler.js')
      }
    },
    define: { __INTLIFY_JIT_COMPILATION__: true, __VUE_OPTIONS_API__: true, __VUE_PROD_DEVTOOLS__: false },
    css: { postcss: { plugins: [tailwind({ ...preset, content: [source + '/**/*.{html,vue,js,ts}', sdk + '/components/**/*.{vue,js,ts}'] }), autoprefixer()] } },
    build: { outDir: path.join(ui, 'compiled'), emptyOutDir: true, target: 'es2020', sourcemap: false }
  })
}
