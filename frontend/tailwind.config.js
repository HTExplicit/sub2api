import preset from '../backend/pkg/extensionapi/ui/tailwind.preset.mjs'

export default { ...preset, content: [...preset.content, '../backend/pkg/extensionapi/ui/components/**/*.{vue,js,ts}'] }
