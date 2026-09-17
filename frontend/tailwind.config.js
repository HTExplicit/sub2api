/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,js,ts,jsx,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        gray: {"50": "#fafafa", "100": "#f5f5f5", "200": "#dcdcdc", "300": "#bdbdbd", "400": "#8a8b8d", "500": "#6f6f6f", "600": "#56575a", "700": "#3a3b40", "800": "#26272b", "900": "#111411", "950": "#111111"},
        slate: {"50": "#fafafa", "100": "#f5f5f5", "200": "#dcdcdc", "300": "#bdbdbd", "400": "#8a8b8d", "500": "#6f6f6f", "600": "#56575a", "700": "#3a3b40", "800": "#26272b", "900": "#111411", "950": "#111111"},
        canvas: 'rgb(var(--ui-canvas) / <alpha-value>)',
        surface: 'rgb(var(--ui-surface) / <alpha-value>)',
        raised: 'rgb(var(--ui-raised) / <alpha-value>)',
        line: 'rgb(var(--ui-line) / <alpha-value>)',
        'line-strong': 'rgb(var(--ui-line-strong) / <alpha-value>)',
        ink: 'rgb(var(--ui-ink) / <alpha-value>)',
        muted: 'rgb(var(--ui-muted) / <alpha-value>)',

        // Experiential green accent
        primary: {
          "50": "#eff9f2",
          "100": "#ddf1e3",
          "200": "#b9e3c8",
          "300": "#8fd3a8",
          "400": "#5abd80",
          "500": "#168a49",
          "600": "#137b40",
          "700": "#106536",
          "800": "#12512e",
          "900": "#123f27",
          "950": "#0a2517"
        },
        // Neutral surfaces
        accent: {
          "50": "#fafafa",
          "100": "#f5f5f5",
          "200": "#dcdcdc",
          "300": "#bdbdbd",
          "400": "#8a8b8d",
          "500": "#6f6f6f",
          "600": "#56575a",
          "700": "#3a3b40",
          "800": "#26272b",
          "900": "#111411",
          "950": "#111111"
        },
        // 深色模式背景
        dark: {
          "50": "#fafafa",
          "100": "#ededed",
          "200": "#dcdcdc",
          "300": "#bdbdbd",
          "400": "#8a8b8d",
          "500": "#797a7d",
          "600": "#3a3b40",
          "700": "#26272b",
          "800": "#1b1c1e",
          "900": "#161618",
          "950": "#111111"
        }
      },
      fontFamily: {
        sans: [
          'MiSans',
          'system-ui',
          '-apple-system',
          'BlinkMacSystemFont',
          'Segoe UI',
          'Roboto',
          'Helvetica Neue',
          'Arial',
          'PingFang SC',
          'Hiragino Sans GB',
          'Microsoft YaHei',
          'sans-serif'
        ],
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'Monaco', 'Consolas', 'monospace']
      },
      boxShadow: {
        outline: '0 0 0 1px rgb(var(--ui-line))',
        glass: '0 0 0 1px rgb(var(--ui-line))',
        'glass-sm': '0 0 0 1px rgb(var(--ui-line))',
        glow: 'none', 'glow-lg': 'none', 'inner-glow': 'none',
        card: 'none', 'card-hover': 'none'
      },

      animation: {
        'fade-in': 'fadeIn 0.15s ease-out',
        'slide-up': 'slideUp 0.15s ease-out',
        'slide-down': 'slideDown 0.15s ease-out',
        'slide-in-right': 'slideInRight 0.15s ease-out',
        'scale-in': 'scaleIn 0.15s ease-out',
        'pulse-slow': 'pulse 3s cubic-bezier(0.4, 0, 0.6, 1) infinite',
        shimmer: 'shimmer 2s linear infinite',
        glow: 'none'
      },
      keyframes: {
        fadeIn: {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' }
        },
        slideUp: {
          '0%': { opacity: '0', transform: 'translateY(4px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' }
        },
        slideDown: {
          '0%': { opacity: '0', transform: 'translateY(-4px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' }
        },
        slideInRight: {
          '0%': { opacity: '0', transform: 'translateX(4px)' },
          '100%': { opacity: '1', transform: 'translateX(0)' }
        },
        scaleIn: {
          '0%': { opacity: '0', transform: 'scale(1)' },
          '100%': { opacity: '1', transform: 'scale(1)' }
        },
        shimmer: {
          '0%': { backgroundPosition: '-200% 0' },
          '100%': { backgroundPosition: '200% 0' }
        },

      },
      backdropBlur: {
        xs: '2px'
      },
      borderRadius: {
        '4xl': '2rem'
      }
    }
  },
  plugins: []
}
