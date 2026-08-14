import type { Config } from "tailwindcss";

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
        // Positive/affirmative state (Trilli Sign: signed, sealed, complete).
        success: {
          DEFAULT: "#16A34A",
          foreground: "#ffffff",
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
        // Navy chrome — global top bar + dark header blocks.
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
        // Legacy aliases — existing references to brand-purple/brand-red keep
        // working but resolve to the new primary/destructive tokens.
        "brand-dark": "#1A1F2C",
        "brand-purple": {
          DEFAULT: "oklch(var(--primary) / <alpha-value>)",
          light: "oklch(var(--primary) / <alpha-value>)",
          dark: "oklch(var(--primary) / <alpha-value>)",
        },
        "brand-red": {
          DEFAULT: "oklch(var(--destructive) / <alpha-value>)",
          light: "oklch(var(--destructive) / <alpha-value>)",
          dark: "oklch(var(--destructive) / <alpha-value>)",
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
        // Logo wordmark — Lobster, applied via font-logo utility.
        logo: ["Lobster", "cursive"],
        // Trilli PDF display face — Hanken Grotesk, the free equivalent of
        // Graphik (iLovePDF's tool-name typeface). Applied via font-pdf.
        pdf: ['"Hanken Grotesk"', "Inter", "sans-serif"],
      },
      // Cool navy-tinted shadows. Base color (#14213B) matches the navy ink
      // so shadows feel cohesive with the cool canvas — pure black shadows
      // on cool gray look harsh.
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
