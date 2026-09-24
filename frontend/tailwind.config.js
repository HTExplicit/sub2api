/** Shared UI defaults with site presentation tokens. */
export default {
  "content": [
    "./index.html",
    "./src/**/*.{vue,js,ts,jsx,tsx}"
  ],
  "darkMode": "class",
  "theme": {
    "extend": {
      "colors": {
        "primary": {
          "50": "rgb(var(--theme-color-primary-50, 240 253 250) / <alpha-value>)",
          "100": "rgb(var(--theme-color-primary-100, 204 251 241) / <alpha-value>)",
          "200": "rgb(var(--theme-color-primary-200, 153 246 228) / <alpha-value>)",
          "300": "rgb(var(--theme-color-primary-300, 94 234 212) / <alpha-value>)",
          "400": "rgb(var(--theme-color-primary-400, 45 212 191) / <alpha-value>)",
          "500": "rgb(var(--theme-color-primary-500, 20 184 166) / <alpha-value>)",
          "600": "rgb(var(--theme-color-primary-600, 13 148 136) / <alpha-value>)",
          "700": "rgb(var(--theme-color-primary-700, 15 118 110) / <alpha-value>)",
          "800": "rgb(var(--theme-color-primary-800, 17 94 89) / <alpha-value>)",
          "900": "rgb(var(--theme-color-primary-900, 19 78 74) / <alpha-value>)",
          "950": "rgb(var(--theme-color-primary-950, 4 47 46) / <alpha-value>)"
        },
        "accent": {
          "50": "rgb(var(--theme-color-accent-50, 248 250 252) / <alpha-value>)",
          "100": "rgb(var(--theme-color-accent-100, 241 245 249) / <alpha-value>)",
          "200": "rgb(var(--theme-color-accent-200, 226 232 240) / <alpha-value>)",
          "300": "rgb(var(--theme-color-accent-300, 203 213 225) / <alpha-value>)",
          "400": "rgb(var(--theme-color-accent-400, 148 163 184) / <alpha-value>)",
          "500": "rgb(var(--theme-color-accent-500, 100 116 139) / <alpha-value>)",
          "600": "rgb(var(--theme-color-accent-600, 71 85 105) / <alpha-value>)",
          "700": "rgb(var(--theme-color-accent-700, 51 65 85) / <alpha-value>)",
          "800": "rgb(var(--theme-color-accent-800, 30 41 59) / <alpha-value>)",
          "900": "rgb(var(--theme-color-accent-900, 15 23 42) / <alpha-value>)",
          "950": "rgb(var(--theme-color-accent-950, 2 6 23) / <alpha-value>)"
        },
        "dark": {
          "50": "rgb(var(--theme-color-dark-50, 248 250 252) / <alpha-value>)",
          "100": "rgb(var(--theme-color-dark-100, 241 245 249) / <alpha-value>)",
          "200": "rgb(var(--theme-color-dark-200, 226 232 240) / <alpha-value>)",
          "300": "rgb(var(--theme-color-dark-300, 203 213 225) / <alpha-value>)",
          "400": "rgb(var(--theme-color-dark-400, 148 163 184) / <alpha-value>)",
          "500": "rgb(var(--theme-color-dark-500, 100 116 139) / <alpha-value>)",
          "600": "rgb(var(--theme-color-dark-600, 71 85 105) / <alpha-value>)",
          "700": "rgb(var(--theme-color-dark-700, 51 65 85) / <alpha-value>)",
          "800": "rgb(var(--theme-color-dark-800, 30 41 59) / <alpha-value>)",
          "900": "rgb(var(--theme-color-dark-900, 15 23 42) / <alpha-value>)",
          "950": "rgb(var(--theme-color-dark-950, 2 6 23) / <alpha-value>)"
        },
        "gray": {
          "50": "rgb(var(--theme-color-gray-50, 249 250 251) / <alpha-value>)",
          "100": "rgb(var(--theme-color-gray-100, 243 244 246) / <alpha-value>)",
          "200": "rgb(var(--theme-color-gray-200, 229 231 235) / <alpha-value>)",
          "300": "rgb(var(--theme-color-gray-300, 209 213 219) / <alpha-value>)",
          "400": "rgb(var(--theme-color-gray-400, 156 163 175) / <alpha-value>)",
          "500": "rgb(var(--theme-color-gray-500, 107 114 128) / <alpha-value>)",
          "600": "rgb(var(--theme-color-gray-600, 75 85 99) / <alpha-value>)",
          "700": "rgb(var(--theme-color-gray-700, 55 65 81) / <alpha-value>)",
          "800": "rgb(var(--theme-color-gray-800, 31 41 55) / <alpha-value>)",
          "900": "rgb(var(--theme-color-gray-900, 17 24 39) / <alpha-value>)",
          "950": "rgb(var(--theme-color-gray-950, 3 7 18) / <alpha-value>)"
        },
        "slate": {
          "50": "rgb(var(--theme-color-slate-50, 248 250 252) / <alpha-value>)",
          "100": "rgb(var(--theme-color-slate-100, 241 245 249) / <alpha-value>)",
          "200": "rgb(var(--theme-color-slate-200, 226 232 240) / <alpha-value>)",
          "300": "rgb(var(--theme-color-slate-300, 203 213 225) / <alpha-value>)",
          "400": "rgb(var(--theme-color-slate-400, 148 163 184) / <alpha-value>)",
          "500": "rgb(var(--theme-color-slate-500, 100 116 139) / <alpha-value>)",
          "600": "rgb(var(--theme-color-slate-600, 71 85 105) / <alpha-value>)",
          "700": "rgb(var(--theme-color-slate-700, 51 65 85) / <alpha-value>)",
          "800": "rgb(var(--theme-color-slate-800, 30 41 59) / <alpha-value>)",
          "900": "rgb(var(--theme-color-slate-900, 15 23 42) / <alpha-value>)",
          "950": "rgb(var(--theme-color-slate-950, 2 6 23) / <alpha-value>)"
        },
        "canvas": "rgb(var(--ui-canvas) / <alpha-value>)",
        "surface": "rgb(var(--ui-surface) / <alpha-value>)",
        "raised": "rgb(var(--ui-raised) / <alpha-value>)",
        "line": "rgb(var(--ui-line) / <alpha-value>)",
        "line-strong": "rgb(var(--ui-line-strong) / <alpha-value>)",
        "ink": "rgb(var(--ui-ink) / <alpha-value>)",
        "muted": "rgb(var(--ui-muted) / <alpha-value>)"
      },
      "fontFamily": {
        "sans": [
          "var(--ui-font)"
        ],
        "mono": [
          "ui-monospace",
          "SFMono-Regular",
          "Menlo",
          "Monaco",
          "Consolas",
          "monospace"
        ]
      },
      "boxShadow": {
        "sm": "var(--theme-shadow-sm, 0 1px 2px 0 rgb(0 0 0 / 0.05))",
        "DEFAULT": "var(--theme-shadow-default, 0 1px 3px 0 rgb(0 0 0 / 0.1), 0 1px 2px -1px rgb(0 0 0 / 0.1))",
        "md": "var(--theme-shadow-md, 0 4px 6px -1px rgb(0 0 0 / 0.1), 0 2px 4px -2px rgb(0 0 0 / 0.1))",
        "lg": "var(--theme-shadow-lg, 0 10px 15px -3px rgb(0 0 0 / 0.1), 0 4px 6px -4px rgb(0 0 0 / 0.1))",
        "xl": "var(--theme-shadow-xl, 0 20px 25px -5px rgb(0 0 0 / 0.1), 0 8px 10px -6px rgb(0 0 0 / 0.1))",
        "2xl": "var(--theme-shadow-2xl, 0 25px 50px -12px rgb(0 0 0 / 0.25))",
        "inner": "var(--theme-shadow-inner, inset 0 2px 4px 0 rgb(0 0 0 / 0.05))",
        "none": "var(--theme-shadow-none, none)",
        "glass": "var(--theme-shadow-glass, 0 8px 32px rgba(0, 0, 0, 0.08))",
        "glass-sm": "var(--theme-shadow-glass-sm, 0 4px 16px rgba(0, 0, 0, 0.06))",
        "glow": "var(--theme-shadow-glow, 0 0 20px rgba(20, 184, 166, 0.25))",
        "glow-lg": "var(--theme-shadow-glow-lg, 0 0 40px rgba(20, 184, 166, 0.35))",
        "card": "var(--theme-shadow-card, 0 1px 3px rgba(0, 0, 0, 0.04), 0 1px 2px rgba(0, 0, 0, 0.06))",
        "card-hover": "var(--theme-shadow-card-hover, 0 10px 40px rgba(0, 0, 0, 0.08))",
        "inner-glow": "var(--theme-shadow-inner-glow, inset 0 1px 0 rgba(255, 255, 255, 0.1))",
        "outline": "var(--theme-shadow-outline, 0 0 0 1px rgb(var(--ui-line)))"
      },
      "backgroundImage": {
        "none": "none",
        "gradient-to-t": "var(--theme-background-gradient-to-t, linear-gradient(to top, var(--tw-gradient-stops)))",
        "gradient-to-tr": "var(--theme-background-gradient-to-tr, linear-gradient(to top right, var(--tw-gradient-stops)))",
        "gradient-to-r": "var(--theme-background-gradient-to-r, linear-gradient(to right, var(--tw-gradient-stops)))",
        "gradient-to-br": "var(--theme-background-gradient-to-br, linear-gradient(to bottom right, var(--tw-gradient-stops)))",
        "gradient-to-b": "var(--theme-background-gradient-to-b, linear-gradient(to bottom, var(--tw-gradient-stops)))",
        "gradient-to-bl": "var(--theme-background-gradient-to-bl, linear-gradient(to bottom left, var(--tw-gradient-stops)))",
        "gradient-to-l": "var(--theme-background-gradient-to-l, linear-gradient(to left, var(--tw-gradient-stops)))",
        "gradient-to-tl": "var(--theme-background-gradient-to-tl, linear-gradient(to top left, var(--tw-gradient-stops)))",
        "gradient-radial": "var(--theme-background-gradient-radial, radial-gradient(var(--tw-gradient-stops)))",
        "gradient-primary": "var(--theme-background-gradient-primary, linear-gradient(135deg, #14b8a6 0%, #0d9488 100%))",
        "gradient-dark": "var(--theme-background-gradient-dark, linear-gradient(135deg, #1e293b 0%, #0f172a 100%))",
        "gradient-glass": "var(--theme-background-gradient-glass, linear-gradient(135deg, rgba(255,255,255,0.1) 0%, rgba(255,255,255,0.05) 100%))",
        "mesh-gradient": "var(--theme-background-mesh-gradient, radial-gradient(at 40% 20%, rgba(20, 184, 166, 0.12) 0px, transparent 50%), radial-gradient(at 80% 0%, rgba(6, 182, 212, 0.08) 0px, transparent 50%), radial-gradient(at 0% 50%, rgba(20, 184, 166, 0.08) 0px, transparent 50%))"
      },
      "animation": {
        "fade-in": "var(--theme-animation-fade-in, fadeIn 0.3s ease-out)",
        "slide-up": "var(--theme-animation-slide-up, slideUp 0.3s ease-out)",
        "slide-down": "var(--theme-animation-slide-down, slideDown 0.3s ease-out)",
        "slide-in-right": "var(--theme-animation-slide-in-right, slideInRight 0.3s ease-out)",
        "scale-in": "var(--theme-animation-scale-in, scaleIn 0.2s ease-out)",
        "pulse-slow": "var(--theme-animation-pulse-slow, pulse 3s cubic-bezier(0.4, 0, 0.6, 1) infinite)",
        "shimmer": "var(--theme-animation-shimmer, shimmer 2s linear infinite)",
        "glow": "var(--theme-animation-glow, glow 2s ease-in-out infinite alternate)"
      },
      "keyframes": {
        "fadeIn": {
          "0%": {
            "opacity": "0"
          },
          "100%": {
            "opacity": "1"
          }
        },
        "slideUp": {
          "0%": {
            "opacity": "0",
            "transform": "translateY(10px)"
          },
          "100%": {
            "opacity": "1",
            "transform": "translateY(0)"
          }
        },
        "slideDown": {
          "0%": {
            "opacity": "0",
            "transform": "translateY(-10px)"
          },
          "100%": {
            "opacity": "1",
            "transform": "translateY(0)"
          }
        },
        "slideInRight": {
          "0%": {
            "opacity": "0",
            "transform": "translateX(20px)"
          },
          "100%": {
            "opacity": "1",
            "transform": "translateX(0)"
          }
        },
        "scaleIn": {
          "0%": {
            "opacity": "0",
            "transform": "scale(0.95)"
          },
          "100%": {
            "opacity": "1",
            "transform": "scale(1)"
          }
        },
        "shimmer": {
          "0%": {
            "backgroundPosition": "-200% 0"
          },
          "100%": {
            "backgroundPosition": "200% 0"
          }
        },
        "glow": {
          "0%": {
            "boxShadow": "0 0 20px rgba(20, 184, 166, 0.25)"
          },
          "100%": {
            "boxShadow": "0 0 30px rgba(20, 184, 166, 0.4)"
          }
        }
      },
      "backdropBlur": {
        "xs": "2px"
      },
      "borderRadius": {
        "none": "0px",
        "sm": "var(--theme-radius-sm, 0.125rem)",
        "DEFAULT": "var(--theme-radius-default, 0.25rem)",
        "md": "var(--theme-radius-md, 0.375rem)",
        "lg": "var(--theme-radius-lg, 0.5rem)",
        "xl": "var(--theme-radius-xl, 0.75rem)",
        "2xl": "var(--theme-radius-2xl, 1rem)",
        "3xl": "var(--theme-radius-3xl, 1.5rem)",
        "full": "9999px",
        "4xl": "var(--theme-radius-4xl, 2rem)"
      }
    }
  },
  "plugins": []
}
