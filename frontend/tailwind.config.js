/**
 * Tailwind mapping for the console theme.
 *
 * Every palette, radius, shadow, gradient, blur and motion value resolves through a CSS variable
 * whose fallback is the upstream (v0.2.8) value. styles/flat-theme.css defines those variables
 * while <html> carries `flat-theme` (public setting flat_theme_enabled), so upstream class names
 * render the console look and switching the setting off restores the upstream look.
 *
 * Colour roles: neutral families (gray/slate/zinc/neutral/stone, dark) resolve per utility
 * (fill, line, text) because upstream uses one shade for several roles; hue families fold into
 * five semantic families (danger, warning, info, success, purple); `primary` is the ink action
 * colour. Solid primary fills export `--theme-on-fill`, which `text-white` reads, so white text on
 * a primary fill follows the fill in dark mode (near-white fill, near-black text).
 */
import defaultColors from 'tailwindcss/colors'
import plugin from 'tailwindcss/plugin'

const SHADES = ['50', '100', '200', '300', '400', '500', '600', '700', '800', '900', '950']

// Upstream palettes that are not Tailwind defaults (fd80b08c9:frontend/tailwind.config.js).
const UPSTREAM_PRIMARY = {
  50: '#f0fdfa', 100: '#ccfbf1', 200: '#99f6e4', 300: '#5eead4', 400: '#2dd4bf', 500: '#14b8a6',
  600: '#0d9488', 700: '#0f766e', 800: '#115e59', 900: '#134e4a', 950: '#042f2e'
}
const UPSTREAM_SLATE = {
  50: '#f8fafc', 100: '#f1f5f9', 200: '#e2e8f0', 300: '#cbd5e1', 400: '#94a3b8', 500: '#64748b',
  600: '#475569', 700: '#334155', 800: '#1e293b', 900: '#0f172a', 950: '#020617'
}

// Tailwind family -> console variable family.
const NEUTRAL_FAMILIES = { gray: 'gray', slate: 'gray', zinc: 'gray', neutral: 'gray', stone: 'gray' }
const HUE_FAMILIES = {
  red: 'danger', rose: 'danger',
  amber: 'warning', yellow: 'warning', orange: 'warning',
  blue: 'info', sky: 'info', indigo: 'info', cyan: 'info',
  green: 'success', emerald: 'success', teal: 'success', lime: 'success',
  purple: 'purple', violet: 'purple', fuchsia: 'purple', pink: 'purple'
}

function channels(hex) {
  const value = hex.replace('#', '')
  return [0, 2, 4].map((i) => parseInt(value.slice(i, i + 2), 16)).join(' ')
}

const rgbVar = (name, fallback) => `rgb(var(${name}, ${fallback}) / <alpha-value>)`

/** Fill role: `--theme-color-<family>-<shade>`, upstream value as fallback. */
function palette(upstream, family) {
  return Object.fromEntries(
    SHADES.map((s) => [s, rgbVar(`--theme-color-${family}-${s}`, channels(upstream[s]))])
  )
}

/** Line/text role: `--theme-<role>-<family>-<shade>`, then the fill variable, then upstream. */
function rolePalette(upstream, family, role) {
  return Object.fromEntries(
    SHADES.map((s) => [
      s,
      rgbVar(`--theme-${role}-${family}-${s}`, `var(--theme-color-${family}-${s}, ${channels(upstream[s])})`)
    ])
  )
}

function neutralColors(role) {
  const out = {}
  for (const [name, family] of Object.entries(NEUTRAL_FAMILIES)) {
    out[name] = role ? rolePalette(defaultColors[name], family, role) : palette(defaultColors[name], family)
  }
  out.accent = role ? rolePalette(UPSTREAM_SLATE, 'gray', role) : palette(UPSTREAM_SLATE, 'gray')
  out.dark = role ? rolePalette(UPSTREAM_SLATE, 'dark', role) : palette(UPSTREAM_SLATE, 'dark')
  return out
}

const hueColors = Object.fromEntries(
  Object.entries(HUE_FAMILIES).map(([name, family]) => [name, palette(defaultColors[name], family)])
)

// Focus rings (ring-primary-400..700) use a neutral focus colour; soft rings keep the ramp.
const primaryRing = Object.fromEntries(
  ['400', '500', '600', '700'].map((s) => [
    s,
    rgbVar('--theme-ring-primary', `var(--theme-color-primary-${s}, ${channels(UPSTREAM_PRIMARY[s])})`)
  ])
)

const role = (name) => `rgb(var(--ui-${name}) / <alpha-value>)`
const lineColors = { ...neutralColors('line') }

// Solid primary fills (and gradient start stops, which the console theme renders as solid)
// publish the foreground for text-white on them.
// Lookup-only: translucent fills (`bg-primary-500/10`) and arbitrary values do not match.
const ON_FILL_SHADES = ['400', '500', '600', '700', '800', '900', '950']
const onPrimaryFill = plugin(({ matchUtilities }) => {
  const values = Object.fromEntries(ON_FILL_SHADES.map((s) => [`primary-${s}`, s]))
  const onFill = () => ({ '--theme-on-fill': 'var(--ui-on-primary, 255 255 255)' })
  matchUtilities({ bg: onFill, from: onFill }, { values, type: ['lookup'] })
})

const themeVar = (name, fallback) => `var(--theme-${name}, ${fallback})`

export default {
  content: ['./index.html', './src/**/*.{vue,js,ts,jsx,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        primary: palette(UPSTREAM_PRIMARY, 'primary'),
        ...neutralColors(),
        ...hueColors,
        canvas: role('canvas'),
        surface: role('surface'),
        subtle: role('subtle'),
        sunk: role('sunk'),
        raised: role('raised'),
        line: role('line'),
        'line-strong': role('line-strong'),
        'line-focus': role('line-focus'),
        ink: role('ink'),
        muted: role('muted'),
        faint: role('faint')
      },
      textColor: {
        ...neutralColors('text'),
        primary: rolePalette(UPSTREAM_PRIMARY, 'primary', 'text'),
        white: rgbVar('--theme-on-fill', '255 255 255')
      },
      borderColor: lineColors,
      ringColor: { ...lineColors, primary: primaryRing },
      outlineColor: { ...lineColors, primary: primaryRing },
      fontFamily: {
        sans: ['var(--ui-font)'],
        mono: ['var(--ui-font-mono)']
      },
      fontSize: {
        sm: [themeVar('text-sm', '0.875rem'), { lineHeight: themeVar('text-sm-leading', '1.25rem') }]
      },
      // No CSS fallbacks here: Tailwind parses shadow values to build the coloured variant
      // (shadow-*-500/30), and a bare var() keeps that variant on the theme value too.
      // Upstream defaults live in styles/theme.css.
      boxShadow: {
        sm: 'var(--theme-shadow-sm)',
        DEFAULT: 'var(--theme-shadow-default)',
        md: 'var(--theme-shadow-md)',
        lg: 'var(--theme-shadow-lg)',
        xl: 'var(--theme-shadow-xl)',
        '2xl': 'var(--theme-shadow-2xl)',
        inner: 'var(--theme-shadow-inner)',
        none: 'none',
        glass: 'var(--theme-shadow-glass)',
        'glass-sm': 'var(--theme-shadow-glass-sm)',
        glow: 'var(--theme-shadow-glow)',
        'glow-lg': 'var(--theme-shadow-glow-lg)',
        card: 'var(--theme-shadow-card)',
        'card-hover': 'var(--theme-shadow-card-hover)',
        'inner-glow': 'var(--theme-shadow-inner-glow)',
        outline: 'var(--theme-shadow-outline)',
        popover: 'var(--ui-shadow-popover)',
        overlay: 'var(--ui-shadow-overlay)'
      },
      backgroundImage: {
        'gradient-to-t': themeVar('background-gradient-to-t', 'linear-gradient(to top, var(--tw-gradient-stops))'),
        'gradient-to-tr': themeVar('background-gradient-to-tr', 'linear-gradient(to top right, var(--tw-gradient-stops))'),
        'gradient-to-r': themeVar('background-gradient-to-r', 'linear-gradient(to right, var(--tw-gradient-stops))'),
        'gradient-to-br': themeVar('background-gradient-to-br', 'linear-gradient(to bottom right, var(--tw-gradient-stops))'),
        'gradient-to-b': themeVar('background-gradient-to-b', 'linear-gradient(to bottom, var(--tw-gradient-stops))'),
        'gradient-to-bl': themeVar('background-gradient-to-bl', 'linear-gradient(to bottom left, var(--tw-gradient-stops))'),
        'gradient-to-l': themeVar('background-gradient-to-l', 'linear-gradient(to left, var(--tw-gradient-stops))'),
        'gradient-to-tl': themeVar('background-gradient-to-tl', 'linear-gradient(to top left, var(--tw-gradient-stops))'),
        'gradient-radial': themeVar('background-gradient-radial', 'radial-gradient(var(--tw-gradient-stops))'),
        'gradient-primary': themeVar('background-gradient-primary', 'linear-gradient(135deg, #14b8a6 0%, #0d9488 100%)'),
        'gradient-dark': themeVar('background-gradient-dark', 'linear-gradient(135deg, #1e293b 0%, #0f172a 100%)'),
        'gradient-glass': themeVar(
          'background-gradient-glass',
          'linear-gradient(135deg, rgba(255,255,255,0.1) 0%, rgba(255,255,255,0.05) 100%)'
        ),
        'mesh-gradient': themeVar(
          'background-mesh-gradient',
          'radial-gradient(at 40% 20%, rgba(20, 184, 166, 0.12) 0px, transparent 50%), radial-gradient(at 80% 0%, rgba(6, 182, 212, 0.08) 0px, transparent 50%), radial-gradient(at 0% 50%, rgba(20, 184, 166, 0.08) 0px, transparent 50%)'
        )
      },
      backdropBlur: {
        xs: themeVar('backdrop-blur-xs', '2px'),
        sm: themeVar('backdrop-blur-sm', '4px'),
        DEFAULT: themeVar('backdrop-blur-default', '8px'),
        md: themeVar('backdrop-blur-md', '12px'),
        lg: themeVar('backdrop-blur-lg', '16px'),
        xl: themeVar('backdrop-blur-xl', '24px'),
        '2xl': themeVar('backdrop-blur-2xl', '40px'),
        '3xl': themeVar('backdrop-blur-3xl', '64px')
      },
      scale: {
        105: themeVar('scale-105', '1.05'),
        110: themeVar('scale-110', '1.1'),
        125: themeVar('scale-125', '1.25'),
        150: themeVar('scale-150', '1.5')
      },
      animation: {
        'fade-in': themeVar('animation-fade-in', 'fadeIn 0.3s ease-out'),
        'slide-up': themeVar('animation-slide-up', 'slideUp 0.3s ease-out'),
        'slide-down': themeVar('animation-slide-down', 'slideDown 0.3s ease-out'),
        'slide-in-right': themeVar('animation-slide-in-right', 'slideInRight 0.3s ease-out'),
        'scale-in': themeVar('animation-scale-in', 'scaleIn 0.2s ease-out'),
        'pulse-slow': themeVar('animation-pulse-slow', 'pulse 3s cubic-bezier(0.4, 0, 0.6, 1) infinite'),
        shimmer: themeVar('animation-shimmer', 'shimmer 2s linear infinite'),
        glow: themeVar('animation-glow', 'glow 2s ease-in-out infinite alternate')
      },
      keyframes: {
        fadeIn: {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' }
        },
        slideUp: {
          '0%': { opacity: '0', transform: 'translateY(10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' }
        },
        slideDown: {
          '0%': { opacity: '0', transform: 'translateY(-10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' }
        },
        slideInRight: {
          '0%': { opacity: '0', transform: 'translateX(20px)' },
          '100%': { opacity: '1', transform: 'translateX(0)' }
        },
        scaleIn: {
          '0%': { opacity: '0', transform: 'scale(0.95)' },
          '100%': { opacity: '1', transform: 'scale(1)' }
        },
        shimmer: {
          '0%': { backgroundPosition: '-200% 0' },
          '100%': { backgroundPosition: '200% 0' }
        },
        glow: {
          '0%': { boxShadow: '0 0 20px rgba(20, 184, 166, 0.25)' },
          '100%': { boxShadow: '0 0 30px rgba(20, 184, 166, 0.4)' }
        }
      },
      borderRadius: {
        sm: themeVar('radius-sm', '0.125rem'),
        DEFAULT: themeVar('radius-default', '0.25rem'),
        md: themeVar('radius-md', '0.375rem'),
        lg: themeVar('radius-lg', '0.5rem'),
        xl: themeVar('radius-xl', '0.75rem'),
        '2xl': themeVar('radius-2xl', '1rem'),
        '3xl': themeVar('radius-3xl', '1.5rem'),
        '4xl': themeVar('radius-4xl', '2rem')
      }
    }
  },
  plugins: [onPrimaryFill]
}
