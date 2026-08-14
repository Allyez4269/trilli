import type { Config } from "tailwindcss";

// CMX inherits app.trilli.com's design system (SPEC §5). The single deliberate
// departure is the brand color: where the app uses indigo (#635BFF), CMX uses a
// muted slate (#565667 ≈ oklch(0.46 0.02 290)). Every other token is identical.
export default {
  darkMode: ["class"],
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        border: "oklch(var(--border) / <alpha-value>)",
        input: "oklch(var(--input) / <alpha-value>)",
        ring: "oklch(var(--ring) / <alpha-value>)",
        background: "oklch(var(--background) / <alpha-value>)",
        foreground: "oklch(var(--foreground) / <alpha-value>)",
        primary: {
          DEFAULT: "oklch(var(--primary) / <alpha-value>)",
          foreground: "oklch(var(--primary-foreground) / <alpha-value>)",
        },
        secondary: {
          DEFAULT: "oklch(var(--secondary) / <alpha-value>)",
          foreground: "oklch(var(--secondary-foreground) / <alpha-value>)",
        },
        destructive: {
          DEFAULT: "oklch(var(--destructive) / <alpha-value>)",
          foreground: "oklch(var(--destructive-foreground) / <alpha-value>)",
        },
        muted: {
          DEFAULT: "oklch(var(--muted) / <alpha-value>)",
          foreground: "oklch(var(--muted-foreground) / <alpha-value>)",
        },
        accent: {
          DEFAULT: "oklch(var(--accent) / <alpha-value>)",
          foreground: "oklch(var(--accent-foreground) / <alpha-value>)",
        },
        popover: {
          DEFAULT: "oklch(var(--popover) / <alpha-value>)",
          foreground: "oklch(var(--popover-foreground) / <alpha-value>)",
        },
        card: {
          DEFAULT: "oklch(var(--card) / <alpha-value>)",
          foreground: "oklch(var(--card-foreground) / <alpha-value>)",
        },
        chrome: {
          DEFAULT: "oklch(var(--chrome) / <alpha-value>)",
          foreground: "oklch(var(--chrome-foreground) / <alpha-value>)",
        },
        sidebar: {
          DEFAULT: "oklch(var(--sidebar) / <alpha-value>)",
          foreground: "oklch(var(--sidebar-foreground) / <alpha-value>)",
          primary: "oklch(var(--sidebar-primary) / <alpha-value>)",
          "primary-foreground":
            "oklch(var(--sidebar-primary-foreground) / <alpha-value>)",
          accent: "oklch(var(--sidebar-accent) / <alpha-value>)",
          "accent-foreground":
            "oklch(var(--sidebar-accent-foreground) / <alpha-value>)",
          border: "oklch(var(--sidebar-border) / <alpha-value>)",
          ring: "oklch(var(--sidebar-ring) / <alpha-value>)",
        },
      },
      borderRadius: {
        xl: "calc(var(--radius) + 4px)",
        lg: "var(--radius)",
        md: "calc(var(--radius) - 2px)",
        sm: "calc(var(--radius) - 4px)",
      },
      fontFamily: {
        sans: [
          "Inter",
          "Geist",
          "-apple-system",
          "BlinkMacSystemFont",
          "Segoe UI",
          "sans-serif",
        ],
      },
      // Cool navy-tinted shadows (design system §4.2). Base color (#14213B)
      // matches the navy ink so shadows feel cohesive on the cool canvas.
      boxShadow: {
        sm: "0 1px 2px rgba(20, 33, 59, 0.06)",
        DEFAULT:
          "0 1px 2px rgba(20, 33, 59, 0.05), 0 4px 12px rgba(20, 33, 59, 0.06)",
        md: "0 4px 12px rgba(20, 33, 59, 0.07), 0 8px 24px rgba(20, 33, 59, 0.05)",
        lg: "0 8px 24px rgba(20, 33, 59, 0.09), 0 16px 48px rgba(20, 33, 59, 0.07)",
        xl: "0 16px 48px rgba(20, 33, 59, 0.11), 0 32px 64px rgba(20, 33, 59, 0.07)",
        "2xl": "0 32px 64px rgba(20, 33, 59, 0.15)",
        inner: "inset 0 1px 2px rgba(20, 33, 59, 0.07)",
      },
    },
  },
  plugins: [require("tailwindcss-animate")],
} satisfies Config;
