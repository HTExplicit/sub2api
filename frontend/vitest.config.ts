import { defineConfig } from 'vitest/config'
import { resolve } from 'path'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  // Match the host and public plugin builds: runtime-only i18n uses CSP-safe JIT.
  define: { __INTLIFY_JIT_COMPILATION__: true },
  server: { fs: { allow: [resolve(__dirname, '..')] } },
  resolve: {
    dedupe: ['vue', 'vue-i18n', '@vue/test-utils', 'vitest', 'vue-draggable-plus'],
    alias: {
      '@': resolve(__dirname, 'src'),
      '@sub2api/plugin-ui': resolve(__dirname, '../backend/pkg/extensionapi/ui'),
      'vue': resolve(__dirname, 'node_modules/vue/dist/vue.runtime.esm-bundler.js'),
      'vue-i18n': 'vue-i18n/dist/vue-i18n.runtime.esm-bundler.js'
    }
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./src/__tests__/setup.ts'],
    include: ['src/**/*.{test,spec}.{js,ts,jsx,tsx}', '../plugins/*/ui/src/**/*.{test,spec}.{js,ts,jsx,tsx}'],
    exclude: ['node_modules', 'dist'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'json', 'html'],
      include: ['src/**/*.{js,ts,vue}'],
      exclude: [
        'node_modules',
        'src/**/*.d.ts',
        'src/**/*.spec.ts',
        'src/**/*.test.ts',
        'src/main.ts'
      ],
      thresholds: {
        global: {
          statements: 80,
          branches: 80,
          functions: 80,
          lines: 80
        }
      }
    }
  }
})
