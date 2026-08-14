# The Cool-Indigo Design System

A complete, reusable design and styling specification — distilled from the Trilli
web application (`app.trilli.com`) but written to be **dropped into any project**.
It documents every layer: the token foundation, typography, spacing, the full
component vocabulary (buttons, forms, tables, modals, menus, banners, badges,
meters), the application shell + navigation, the authentication screens, motion,
and iconography.

The stack it was authored against is **React 18 + TypeScript + Vite + Tailwind
CSS 3 + Radix primitives (shadcn-style) + lucide-react icons + CVA + tailwind-merge**,
but the *visual system* (tokens, recipes, spacing rules) is framework-agnostic —
the OKLCH variables and class recipes can be reproduced in any Tailwind project,
and the principles translate to plain CSS.

---

## Table of Contents

1. [Design Principles](#1-design-principles)
2. [Color Tokens (OKLCH)](#2-color-tokens-oklch)
3. [Typography](#3-typography)
4. [Spacing, Radius & Elevation](#4-spacing-radius--elevation)
5. [The 90% Desktop Zoom Mechanic](#5-the-90-desktop-zoom-mechanic)
6. [Iconography](#6-iconography)
7. [Application Shell & Layout](#7-application-shell--layout)
8. [Navigation](#8-navigation)
9. [Buttons](#9-buttons)
10. [Forms & Inputs](#10-forms--inputs)
11. [Cards & Surfaces](#11-cards--surfaces)
12. [Tables & Lists](#12-tables--lists)
13. [Modals, Overlays & Dialogs](#13-modals-overlays--dialogs)
14. [Menus & Tooltips](#14-menus--tooltips)
15. [Banners & Info Strips](#15-banners--info-strips)
16. [Badges, Chips & Status Pills](#16-badges-chips--status-pills)
17. [Meters, Progress & Sparklines](#17-meters-progress--sparklines)
18. [Dashboard & Stat Cards](#18-dashboard--stat-cards)
19. [Empty States & Skeletons](#19-empty-states--skeletons)
20. [Avatars](#20-avatars)
21. [Authentication & Onboarding Screens](#21-authentication--onboarding-screens)
22. [Motion & Animation](#22-motion--animation)
23. [Brand & Logo](#23-brand--logo)
24. [Porting Checklist](#24-porting-checklist)

---

## 1. Design Principles

The system is a **cool, "Stripe-style" SaaS aesthetic**. Five rules govern it:

1. **Two cool hues, one neutral ramp.** A single saturated indigo "blurple"
   (`#635BFF`) carries every call-to-action, active state, focus ring, and chart
   line. A deep navy (`#0A2540`) owns "chrome" (the global top bar, dark headers).
   Everything else is a cool, slightly blue-tinted gray ramp on a near-white canvas.
   Never introduce a third brand hue; semantic accents (emerald/amber/red) are for
   *state*, not branding.

2. **White cards float on a tinted canvas.** The app background is a cool
   near-white (`#f6f8fa`); content surfaces are **pure white** (`#ffffff`) and
   "punch out" of the canvas via a 1px hairline border + a soft, navy-tinted
   shadow. Depth is communicated by elevation, not by heavy outlines.

3. **Restraint in weight and size.** Body text is small and tight; controls are
   compact (default control height is short). Emphasis comes from **weight and
   color**, not from large type. Numbers use `tabular-nums`.

4. **Active state = inset pill + accent bar, never a full-row background.**
   Navigation highlights are drawn as an absolutely-positioned inset pill plus a
   3px accent bar, so the highlight is visually shorter than the click target.

5. **Tokens, not literals.** Every color is a CSS custom property consumed through
   Tailwind. The only sanctioned literal colors are *semantic state* colors
   (emerald/amber/red/sky/violet families) and two `oklch()` gradient stops.

---

## 2. Color Tokens (OKLCH)

All colors are defined as **OKLCH triples** (`L C H` — lightness, chroma, hue)
in a `:root` block and exposed to Tailwind with the alpha-modifier pattern so
`bg-primary/90`, `text-foreground/70`, etc. all work.

### 2.1 The `:root` definitions

```css
@layer base {
  :root {
    /* Canvas — cool near-white (#f6f8fa). Cards punch out as pure white. */
    --background: 0.985 0.004 255;
    --foreground: 0.26 0.02 270;          /* navy ink */

    /* Surfaces — pure white (#ffffff). */
    --card: 1 0 0;
    --card-foreground: 0.26 0.02 270;
    --popover: 1 0 0;
    --popover-foreground: 0.26 0.02 270;

    /* Primary — indigo "blurple" #635bff. CTAs, active, focus rings, charts. */
    --primary: 0.56 0.24 277;
    --primary-foreground: 0.99 0 0;

    /* Secondary — soft indigo surface #eef0ff (low-emphasis buttons/pills). */
    --secondary: 0.95 0.03 277;
    --secondary-foreground: 0.32 0.06 277;

    /* Muted — cool light gray #f1f4f8 (row hovers, ghost buttons). */
    --muted: 0.96 0.006 255;
    --muted-foreground: 0.50 0.02 265;     /* cool slate (timestamps) */

    /* Accent — lighter violet #8b85ff (small highlights). */
    --accent: 0.62 0.18 285;
    --accent-foreground: 0.99 0 0;

    /* Destructive — notification red #f13744. */
    --destructive: 0.605 0.225 22;
    --destructive-foreground: 0.99 0 0;

    /* Lines + focus rings. Inputs are white so they punch out of cards/tables. */
    --border: 0.92 0.008 255;              /* #e3e8ee hairline */
    --input: 1 0 0;                        /* white */
    --ring: 0.56 0.24 277;                 /* = primary */

    --radius: 0.5rem;

    /* Chrome — deep navy #0a2540 for the global top bar and dark headers. */
    --chrome: 0.25 0.05 258;
    --chrome-foreground: 0.97 0.01 255;

    /* Sidebar — near-white body, indigo primary + soft-indigo hover surfaces. */
    --sidebar: 0.99 0.003 255;
    --sidebar-foreground: 0.26 0.02 270;
    --sidebar-primary: 0.56 0.24 277;
    --sidebar-primary-foreground: 0.99 0 0;
    --sidebar-accent: 0.95 0.02 277;
    --sidebar-accent-foreground: 0.30 0.04 277;
    --sidebar-border: 0.93 0.007 255;
    --sidebar-ring: 0.56 0.24 277;
  }

  * { @apply border-border; }            /* default hairline on every element */
  body { @apply bg-background text-foreground antialiased; }
}
```

### 2.2 Token → hex → role reference

| Token | OKLCH | ≈ Hex | Role |
|---|---|---|---|
| `--background` | `0.985 0.004 255` | `#f6f8fa` | App canvas (cool near-white) |
| `--foreground` | `0.26 0.02 270` | `#1a1f36` | Body text / navy ink |
| `--card` / `--popover` | `1 0 0` | `#ffffff` | Cards, tables, menus, modals |
| `--primary` / `--ring` | `0.56 0.24 277` | `#635bff` | CTA, active, focus, charts |
| `--primary-foreground` | `0.99 0 0` | `#ffffff` | Text on primary |
| `--secondary` | `0.95 0.03 277` | `#eef0ff` | Soft indigo surface / pills |
| `--secondary-foreground` | `0.32 0.06 277` | indigo-navy | Text on secondary |
| `--muted` | `0.96 0.006 255` | `#f1f4f8` | Hover surface, ghost bg, tracks |
| `--muted-foreground` | `0.50 0.02 265` | `#6b7280` | Timestamps, secondary text |
| `--accent` | `0.62 0.18 285` | `#8b85ff` | Small violet highlights |
| `--destructive` | `0.605 0.225 22` | `#f13744` | Errors, delete, over-quota |
| `--border` | `0.92 0.008 255` | `#e3e8ee` | Hairline borders |
| `--input` | `1 0 0` | `#ffffff` | Input fields |
| `--chrome` | `0.25 0.05 258` | `#0a2540` | Global top bar, dark headers |
| `--chrome-foreground` | `0.97 0.01 255` | near-white | Text on chrome |
| `--sidebar` | `0.99 0.003 255` | near-white | Sidebar body |

### 2.3 Tailwind wiring

Each token is registered with the `<alpha-value>` placeholder so opacity
modifiers work:

```ts
// tailwind.config.ts → theme.extend.colors
colors: {
  border:     "oklch(var(--border) / <alpha-value>)",
  input:      "oklch(var(--input) / <alpha-value>)",
  ring:       "oklch(var(--ring) / <alpha-value>)",
  background: "oklch(var(--background) / <alpha-value>)",
  foreground: "oklch(var(--foreground) / <alpha-value>)",
  primary:     { DEFAULT: "oklch(var(--primary) / <alpha-value>)",
                 foreground: "oklch(var(--primary-foreground) / <alpha-value>)" },
  secondary:   { DEFAULT: "...", foreground: "..." },
  muted:       { DEFAULT: "...", foreground: "..." },
  accent:      { DEFAULT: "...", foreground: "..." },
  destructive: { DEFAULT: "...", foreground: "..." },
  popover:     { DEFAULT: "...", foreground: "..." },
  card:        { DEFAULT: "...", foreground: "..." },
  chrome:      { DEFAULT: "...", foreground: "..." },   // navy top bar
  sidebar:     { DEFAULT, foreground, primary, "primary-foreground",
                 accent, "accent-foreground", border, ring },
}
```

### 2.4 Semantic state colors (NOT brand)

State is communicated with stock Tailwind palette families, always as a
**tinted background (`/10`–`/15`) + a `-600`/`-700` text shade**:

| State | Background | Text/Icon | Border |
|---|---|---|---|
| Success / active | `bg-emerald-500/10` (or `/15`) | `text-emerald-600` / `-700` | `border-emerald-500/30` |
| Warning / off | `bg-amber-500/10` (or `/5`) | `text-amber-600` / `-700` | `border-amber-500/30`–`/40` |
| Error / inactive | `bg-destructive/10` or `bg-red-500/15` | `text-destructive` / `text-red-600` | `border-destructive/30` |
| Info | `bg-primary/[0.04]` | `text-primary/70` | `border-primary/15` |

Activity/feed action accents (solid dots): upload `emerald-500`, download
`sky-500`, move/rename `amber-500`, copy/create `indigo-500`, delete `rose-500`,
share `violet-500`, star `amber-400`, view `slate-400`.

---

## 3. Typography

### 3.1 Font stacks

```ts
fontFamily: {
  sans: ["Inter", "Geist", "-apple-system", "BlinkMacSystemFont",
         "Segoe UI", "sans-serif"],
  logo: ["Lobster", "cursive"],   // reserved for a script wordmark; optional
}
```

```css
body {
  font-family: "Inter", "Geist", -apple-system, BlinkMacSystemFont,
               "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
  font-feature-settings: "cv02", "cv03", "cv04", "cv11";   /* Inter stylistic sets */
}
```

The Inter `cv*` features sharpen the `1`, lowercase `a`/`g`/`l` for a crisper
product feel. Always pair numeric/statistic displays with `tabular-nums`.

### 3.2 The type scale in practice

The system runs **small and tight**. Common roles:

| Role | Classes |
|---|---|
| Page title (`PageHeader`/dashboard) | `text-2xl font-bold tracking-tight` (or `font-semibold`) |
| Big page title (Usage) | `text-3xl font-semibold` |
| Card title (shadcn `CardTitle`) | `text-2xl font-semibold leading-none tracking-tight` |
| Section title (in-card) | `text-sm font-semibold text-foreground` |
| Section/group label (eyebrow) | `text-[10.5px]`–`text-xs font-semibold uppercase tracking-wide text-muted-foreground` |
| Stat number | `text-[22px] font-semibold leading-none tabular-nums` |
| Body / default | `text-sm` (14px) |
| Dense body / table cells | `text-[12.5px]` / `text-[13px]` |
| Labels (form) | `text-sm font-medium leading-none` (compact: `text-xs font-medium`) |
| Helper / hint | `text-[11px] text-muted-foreground` |
| Micro (badges, pills) | `text-[10px]`–`text-[11px]`, often `uppercase tracking-wide` |
| Table header | `text-[10.2px] uppercase tracking-wide text-muted-foreground` |

Rule of thumb: **labels and section headers are uppercase + tracked + muted**;
**values and titles are foreground ink**; emphasis steps via `font-medium` →
`font-semibold` → `font-bold` rather than via size jumps.

---

## 4. Spacing, Radius & Elevation

### 4.1 Radius scale

Driven by one variable, `--radius: 0.5rem` (8px):

```ts
borderRadius: {
  xl: "calc(var(--radius) + 4px)",   // 12px — page card, hub cards, large modals
  lg: "var(--radius)",               //  8px — cards, inputs, nav pills, buttons
  md: "calc(var(--radius) - 2px)",   //  6px — buttons, inputs, menus, small ctrls
  sm: "calc(var(--radius) - 4px)",   //  4px — menu items, tiny chips
}
```

Usage convention: **`rounded-xl`** for the floating page card and elevated hub
cards; **`rounded-lg`** for standard cards, inputs, nav pills, icon tiles;
**`rounded-md`** for buttons, dropdowns, small controls; **`rounded-sm`** for menu
items; **`rounded-full`** for pills, avatars, progress tracks, status dots.

### 4.2 Elevation (navy-tinted shadows)

Shadows use the navy ink color (`rgba(20, 33, 59, …)`) — never pure black —
so they feel cohesive on the cool canvas:

```ts
boxShadow: {
  sm:      "0 1px 2px rgba(20,33,59,0.06)",
  DEFAULT: "0 1px 2px rgba(20,33,59,0.05), 0 4px 12px rgba(20,33,59,0.06)",
  md:      "0 4px 12px rgba(20,33,59,0.07), 0 8px 24px rgba(20,33,59,0.05)",
  lg:      "0 8px 24px rgba(20,33,59,0.09), 0 16px 48px rgba(20,33,59,0.07)",
  xl:      "0 16px 48px rgba(20,33,59,0.11), 0 32px 64px rgba(20,33,59,0.07)",
  "2xl":   "0 32px 64px rgba(20,33,59,0.15)",
  inner:   "inset 0 1px 2px rgba(20,33,59,0.07)",
}
```

Elevation language:
- **`shadow-sm`** — resting cards, the page card, inputs.
- **`shadow-md`** — dropdown/context-menu content, the sidebar account-hub card.
- **`shadow-lg`** — auth card, dropdown sub-menus.
- **`shadow-xl`** — standalone modals (Ack/Applied/Share/2FA).
- **`shadow-2xl`** — the shared blur-backed `Overlay` panel.

### 4.3 Spacing rhythm

- **Card padding:** `p-4` (compact tiles) → `p-5` (settings sections) → `p-6`
  (shadcn `CardHeader`/`CardContent`) → `p-8`/`sm:p-10` (auth card).
- **Page padding:** `p-6 lg:p-8` inside the floating content card; dashboards use
  `px-6 py-7 lg:px-8`.
- **Page max-widths:** dashboards `max-w-6xl`; data/settings pages `max-w-7xl`;
  auth card `max-w-[30.8rem]`.
- **Vertical rhythm:** `space-y-2` between a label and its input; `space-y-4`
  within a form; `space-y-5` between form steps/sections; `space-y-6` between
  settings sections; `space-y-8` between major page blocks.
- **Inline gaps:** `gap-1.5`/`gap-2` for icon+text in buttons and chips; `gap-3`
  for list rows; `gap-2` for footer button clusters.

---

## 5. The 90% Desktop Zoom Mechanic

A signature trait: on screens ≥1024px the **entire app renders at 90% scale** so
more content fits, while phones/tablets stay at 100%.

```css
@media (min-width: 1024px) {
  body { zoom: 0.9; }
  /* zoom doesn't scale viewport units, so compensate full-height utilities: */
  .h-screen     { height: calc(100vh / 0.9); }
  .min-h-screen { min-height: calc(100vh / 0.9); }

  /* Radix poppers portal to <body> with a fixed translate that the zoom would
     skew. Counter-zoom the wrapper, restore density on the content. */
  [data-radix-popper-content-wrapper]     { zoom: calc(1 / 0.9); }
  [data-radix-popper-content-wrapper] > * { zoom: 0.9; }
}
```

Consequences to remember when porting:
- `zoom` (not `transform: scale`) is used so layout, scrollbars, and body-portaled
  overlays scale together and keep normal flow.
- **Avoid `ring-`/`border-` for hairline "rings" that must stay crisp** under the
  zoom — e.g. the avatar "ring" is a real padded circle (`bg-[#a6b2d0] p-[3.9px]`)
  rather than a `ring-` utility, to dodge sub-pixel rasterization.
- Full-screen marketing/checkout flows opt **out** of the density with a
  `.signup-fullscreen` class that counter-zooms back to true 100%.

---

## 6. Iconography

- **Library:** [`lucide-react`](https://lucide.dev) exclusively, plus a few
  hand-authored inline SVGs (brand glyphs, a custom file-type badge). Brand
  marks (Google/Microsoft) are multi-color SVGs at their official hexes.
- **Stroke:** default lucide weight; active nav icons bump to `stroke-[2.4]`.
- **Color:** icons inherit `currentColor`. Idle/secondary icons are
  `text-muted-foreground`; active/brand icons are `text-primary`.
- **Standard sizes:**

| Context | Size |
|---|---|
| Inline with text, buttons, menu items, table actions | `h-4 w-4` (`size-4`) |
| Small chips, badges, dense controls, spinners-in-button | `h-3.5 w-3.5` |
| Micro (badge overlays) | `h-3 w-3` / smaller |
| Sidebar nav (expanded / collapsed) | `h-[18px] w-[18px]` / `h-[19px] w-[19px]` |
| Brand/provider glyphs (OAuth) | `h-[18px] w-[18px]` |
| Page-title icon | `h-6 w-6` (big title `h-7 w-7`) |
| Icon tile contents (badges) | `h-[18px] w-[18px]` in an `h-9 w-9` tile |
| Empty-state hero icon | `h-8 w-8` in an `h-16 w-16` tile |

- **Icon tiles** (a recurring motif): a rounded square holding an icon, used as a
  lead graphic. Recipe: `flex h-9 w-9 items-center justify-center rounded-lg
  bg-primary/10 text-primary` (active) or `bg-secondary text-muted-foreground`
  (idle). Hero variant: `h-16 w-16 rounded-2xl bg-primary/10 text-primary`.
- **Spinners:** `<Loader2 className="h-4 w-4 animate-spin" />` inline; `h-6 w-6`
  for page-level loading.

---

## 7. Application Shell & Layout

The authenticated app is a **fixed-height, three-region shell**: a navy chrome bar
on top, a sidebar on the left, and a floating white content card filling the rest.
Every protected route renders inside it (a `ProtectedRoute` wrapper gates auth and
admin access, then wraps children in the shell). Public/auth/onboarding routes
render full-screen *outside* the shell.

### 7.1 Root

```html
<div class="flex h-screen flex-col overflow-hidden bg-background">
  <header> ...chrome bar... </header>
  <div class="flex flex-1 overflow-hidden">
    <aside> ...sidebar... </aside>
    <div class="flex flex-1 flex-col overflow-hidden">
      <main class="relative mb-2 mr-2 mt-2 flex flex-1 flex-col overflow-hidden
                   rounded-xl border border-border bg-card shadow-sm">
        <!-- optional banner -->
        <div class="flex min-h-0 flex-1 flex-col overflow-y-auto"> {children} </div>
        <footer> ...app footer... </footer>
      </main>
    </div>
  </div>
</div>
```

The content `<main>` is a **floating card**: margins `mt-2 mr-2 mb-2` (8px top/
right/bottom, **no left margin** so it butts against the sidebar), `rounded-xl`,
1px border, white bg, `shadow-sm`. The canvas (`bg-background`) shows through the
gaps.

### 7.2 Chrome top bar

```html
<header class="flex h-14 shrink-0 items-center justify-between
               bg-chrome pl-4 pr-5 text-chrome-foreground">
```

- Height `h-14` (56px), navy `bg-chrome`, near-white text.
- **Left:** brand logo link, forced white, `h-[28px]`.
- **Right cluster** (`flex items-center gap-3`), in order:
  1. **Account/context switcher** (only when >1 context) — a translucent chip:
     `inline-flex items-center gap-1.5 rounded-md bg-white/10 px-2.5 py-1.5
     text-[13px] font-medium text-white/90 hover:bg-white/15`, with a leading
     icon, a `max-w-[150px] truncate` label, and a `ChevronDown`.
  2. **Greeting** (`sm:` up): `text-sm text-white/70` with the name in
     `font-medium text-white`.
  3. **Avatar** in a padded periwinkle ring: `<span class="flex rounded-full
     bg-[#a6b2d0] p-[3.9px]"><Avatar class="size-10" …/></span>`.
  4. **Role badge:** `rounded-full bg-white/15 px-2 py-0.5 text-[11px]
     font-medium capitalize text-white`.
  5. **Plan badge** (optional): `inline-flex items-center gap-1 rounded-full
     border border-primary/40 bg-primary/25 px-2.5 py-0.5 text-[11px]
     font-semibold text-white shadow-sm` + a tier icon.
  6. **Notifications bell:** ghost icon button `relative h-9 w-9
     hover:bg-white/10`; unread count badge `absolute -right-0.5 -top-0.5 flex
     h-4 min-w-[16px] items-center justify-center rounded-full bg-destructive
     px-1 text-[10px] font-semibold text-destructive-foreground`.

Translucent-on-navy controls use the `white/10` → `white/15` → `white/25` opacity
ramp for resting → hover → active/drag states.

### 7.3 App footer

```html
<footer class="flex shrink-0 flex-col items-center justify-center gap-1.5
               bg-card/60 px-6 py-3 text-[11px] text-muted-foreground">
```
A centered link row (`gap-x-4`, links `hover:text-foreground`) above a copyright
line; segments separated by a `<span class="text-border">|</span>` pipe.

### 7.4 In-card top banner (lapse/alert)

A full-width strip pinned to the top of the content card:
`flex flex-shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b px-6 py-2.5
text-sm`. Urgent variant `border-destructive/30 bg-destructive/10 text-destructive`;
warning variant `border-amber-500/30 bg-amber-500/10 text-amber-700`. Leads with
an `AlertTriangle h-4 w-4`; a primary action button floats right via `ml-auto`.

---

## 8. Navigation

### 8.1 Sidebar container

```html
<aside class="relative z-30 flex h-full shrink-0 flex-col bg-sidebar
              text-sidebar-foreground transition-[width] duration-300 ease-in-out
              [w-[228px] | w-[60px]]">
```
- Expanded **228px**, collapsed **60px**, animated `transition-[width]
  duration-300 ease-in-out`.
- Collapse state persists in `localStorage`. A toggle button straddles the right
  edge (desktop only): `absolute -right-3 top-5 z-50 hidden h-6 w-6 items-center
  justify-center rounded-full border border-border bg-card text-muted-foreground
  shadow-sm hover:bg-muted md:flex`.
- The brand lives in the chrome bar, **not** the sidebar.

### 8.2 Nav structure

`<nav class="flex-1 space-y-1 overflow-y-auto p-3 pt-4">` holding grouped sections
(e.g. *Main*, *Apps*, *Manage*). Each later group is divided by
`mt-6 pt-4 border-t border-sidebar-border`. Section titles (hidden when collapsed):
`px-3 text-xs font-medium uppercase tracking-wider text-muted-foreground`.
Admin-only groups render only when the user has the right role.

### 8.3 Nav item anatomy (the signature active-state)

The highlight is **layered**, never a background on the clickable row:

```html
<!-- expanded NavLink -->
<a class="group relative flex w-full items-center gap-[11px] rounded-lg
          px-[11px] py-[9px] transition-all duration-200
          [text-sidebar-foreground | active:text-sidebar-accent-foreground]">
  <!-- idle hover layer -->
  <span class="absolute inset-x-0 inset-y-[4.5px] rounded-lg bg-muted opacity-0
               transition-opacity duration-200 group-hover:opacity-100"></span>
  <!-- active: solid fill + 3px accent bar -->
  <span class="absolute inset-x-0 inset-y-[4.5px] rounded-lg bg-muted"></span>
  <span class="absolute inset-y-[4.5px] left-0 w-[3px] rounded-full bg-primary"></span>

  <Icon class="relative h-[18px] w-[18px] flex-shrink-0
               [active:text-primary | idle:text-black]" />
  <span class="relative flex-1 text-left text-[13px] font-medium">{label}</span>
  <!-- optional count badge / "soon" tag -->
</a>
```

- **Idle:** icon ink (`text-black`/`text-muted-foreground`), label `text-[13px]
  font-medium`; hovering fades in a `bg-muted` inset pill.
- **Active:** persistent `bg-muted` inset pill + a **3px `bg-primary` left bar**;
  icon turns `text-primary`.
- The inset pill is `inset-y-[4.5px]` (shorter than the 9px-padded row), so it
  reads as a contained chip inside a larger hit area.
- **Collapsed:** icon-only, centered in an `h-9 w-9 rounded-lg` target, wrapped in
  a right-side tooltip; active icon is `text-primary stroke-[2.4]`.
- Count badge: `rounded-full bg-sidebar-foreground/10 px-1.5 py-0.5 text-[10px]
  font-semibold tabular-nums`. "Soon" tag: `rounded bg-secondary px-1.5 py-0.5
  text-[9px] uppercase tracking-wide text-muted-foreground`.
- Active route is detected via the router's `NavLink isActive` render-prop, not
  manual matching.

### 8.4 Account hub (sidebar footer)

An elevated card pinned to the sidebar bottom:
`overflow-hidden rounded-xl border border-border bg-card shadow-md ring-1
ring-black/[0.02]`. It contains:
- A **storage block** (`space-y-2.5 p-3`): an uppercase `HardDrive` + "Storage"
  label, a `tabular-nums` usage figure, and a progress meter (see §17). An
  upgrade CTA button when applicable.
- A **quick-links block** (`space-y-1 border-t border-border p-2`): rows that
  reuse the same layered hover/active treatment as nav items but at
  `inset-y-[2.5px]`, with a trailing `ChevronRight` or "soon" tag. Logout sits
  last with a `LogOut` icon.

Collapsed, the hub becomes three tooltipped icons.

### 8.5 PageHeader

```html
<header class="flex flex-col gap-6">
  <div>
    <!-- optional eyebrow: mb-1 flex items-center gap-1.5 text-sm text-muted-foreground -->
    <h1 class="flex items-center gap-2 text-2xl font-bold tracking-tight
               text-foreground">{icon?} {title}</h1>
  </div>
  <!-- optional controls row -->
  <div class="flex flex-col items-stretch gap-4 sm:flex-row sm:items-center">
    <form class="relative max-w-md flex-1"> …search… </form>
    <div class="flex items-center gap-2 sm:ml-auto"> …view toggle, New menu… </div>
  </div>
</header>
```
- **Search:** a relative wrapper with an absolutely-positioned leading
  `Search h-4 w-4 text-muted-foreground` at `left-3 top-1/2 -translate-y-1/2`,
  and an `Input` padded `pl-10`.
- **Segmented view toggle:** `flex items-center rounded-lg bg-secondary p-1`; each
  option is a ghost icon button, selected gets `bg-background shadow-sm`.

---

## 9. Buttons

Built with **CVA** (`class-variance-authority`) so variants and sizes compose.

### 9.1 Base

```
inline-flex items-center justify-center whitespace-nowrap rounded-md text-sm
font-medium ring-offset-background transition-colors focus-visible:outline-none
focus-visible:ring-1 focus-visible:ring-primary/40 disabled:pointer-events-none
disabled:opacity-50
```

### 9.2 Variants

| Variant | Classes |
|---|---|
| `default` (primary) | `bg-primary text-primary-foreground hover:bg-primary/90` |
| `destructive` | `bg-destructive text-destructive-foreground hover:bg-destructive/90` |
| `outline` | `border border-input bg-background hover:bg-accent hover:text-accent-foreground` |
| `secondary` | `bg-secondary text-secondary-foreground hover:bg-secondary/80` |
| `ghost` | `hover:bg-accent hover:text-accent-foreground` |
| `link` | `text-primary underline-offset-4 hover:underline` |

### 9.3 Sizes

| Size | Classes |
|---|---|
| `default` | `h-7 px-4 py-2` |
| `sm` | `h-7 rounded-md px-3` |
| `lg` | `h-8 rounded-md px-8` |
| `icon` | `h-7 w-7` |

> **Important:** the base button is intentionally short (`h-7`). Forms and modals
> routinely override to `h-9 w-full`, and prominent CTAs to `h-11`. The base size
> suits dense toolbars; the **`h-9` full-width** form button is the de-facto
> standard for primary submit actions.

### 9.4 Recurring hand-rolled recipes

Outside the CVA component, two recipes recur in modals/settings (note the smaller
text and explicit height):

```
/* Primary action */
inline-flex h-9 items-center gap-1.5 rounded-md bg-primary px-3.5 text-[13px]
font-semibold text-primary-foreground hover:bg-primary/90 disabled:opacity-60

/* Cancel / secondary */
h-9 rounded-md border border-border bg-background px-3 text-[13px] font-medium
text-foreground hover:bg-muted disabled:opacity-60
```

A compact settings/modal button variant uses `h-7 min-w-[…rem] … text-[11px]
font-medium shadow-sm`. When busy, prepend a `<Loader2 className="h-3.5 w-3.5
animate-spin" />` and swap the label ("Save" → "Saving…").

Destructive confirm buttons use `bg-destructive text-white hover:bg-destructive/90`.

---

## 10. Forms & Inputs

### 10.1 Input primitive

```
flex h-7 w-full rounded-md border border-input bg-background px-3 py-2 text-sm
ring-offset-background file:border-0 file:bg-transparent file:text-sm
file:font-medium placeholder:text-muted-foreground focus-visible:outline-none
focus-visible:ring-1 focus-visible:ring-primary/40 disabled:cursor-not-allowed
disabled:opacity-50
```

Because `--input` and `--background` are both **white**, the default border
(`border-input`) is invisible on a white card. So forms standardize on an explicit
visible border:

- **Auth/standard forms:** `const fieldCls = "h-9 border-foreground/25"`.
- **Dense settings forms:** `h-7 max-w-2xl border-foreground/25 bg-background
  text-xs`; read-only fields add `bg-muted/40 text-muted-foreground`.
- **Dialog inputs:** `h-9 w-full rounded-md border bg-background px-2.5 text-[13px]
  outline-none focus-visible:ring-1`, with the border switching to
  `border-destructive focus-visible:ring-destructive/40` on error.

**Focus** is universally a 1px primary ring: `focus-visible:ring-1
focus-visible:ring-primary/40` (dashboards use `focus:ring-2 focus:ring-primary/15`
+ `focus:border-primary/40`).

### 10.2 Label & field group

- **Label:** `text-sm font-medium leading-none` (Radix Label). Compact form
  contexts use `text-xs font-medium text-foreground`.
- **Field group:** wrap each label+control in `<div class="space-y-2">`
  (label-to-control gap). A form is `space-y-4`; steps/sections `space-y-5`.
- A label can pair with an inline action via `flex items-center justify-between`
  (e.g. "Password" + "Forgot your password?" as `text-[12px] text-primary
  hover:underline`).
- **No asterisk required-markers.** Requiredness is enforced via the HTML
  `required` attribute and surfaced through disabled submit buttons + inline
  errors.

### 10.3 Validation & messaging

| Kind | Recipe |
|---|---|
| Inline error (form-level) | `text-sm text-destructive` (`role="alert"`) |
| Inline error (field) | `text-[11px]`/`text-[12px] text-destructive` |
| Boxed error | `rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm` |
| Success notice | `bg-emerald-500/10 text-emerald-700` or `border-emerald-500/30 bg-emerald-500/5` |
| Warning | `border-amber-500/30 bg-amber-500/5` / `text-amber-700` |
| Helper / hint | `text-[11px] text-muted-foreground` |

### 10.4 Checkboxes (size-locked native control)

Native checkboxes are pinned to an exact size (min/max locks) so no browser
renders them larger:

```
/* 10.5px — forms, dense tables */
h-[10.5px] w-[10.5px] min-h-[10.5px] min-w-[10.5px] max-h-[10.5px] max-w-[10.5px]
flex-none align-middle rounded-[3px] border-border text-primary outline-none
focus:outline-none focus-visible:ring-1 focus-visible:ring-primary/40
disabled:cursor-not-allowed disabled:border-muted-foreground/70 disabled:bg-muted

/* 12.6px (+20%) — file/data list rows: same string, 12.6px substituted */
```

### 10.5 Toggle switch

```html
<button class="relative inline-flex h-[17px] w-[31px] flex-shrink-0 items-center
               rounded-full transition-colors [on:bg-primary | off:bg-muted]
               disabled:opacity-50">
  <span class="inline-block h-3.5 w-3.5 rounded-full bg-white shadow
               transition-transform [on:translate-x-[15px] | off:translate-x-0.5]"></span>
</button>
```

### 10.6 Segmented code (OTP) input

```html
<div class="flex items-center justify-center gap-2">   <!-- N boxes, default 6 -->
  <input class="h-12 w-11 rounded-lg border border-foreground/25 bg-background
                text-center font-mono text-xl outline-none transition
                focus:border-primary focus:ring-1 focus:ring-primary/40
                [filled: border-primary/60]
                disabled:cursor-not-allowed disabled:opacity-50"
         inputmode="numeric" maxlength="1" autocomplete="one-time-code" />
  …
</div>
```
Type-to-advance, backspace-to-retreat, arrow navigation, and paste-spreads-code;
fire an `onComplete` when all boxes are filled.

### 10.7 Password strength meter

Four equal segments + a label/hint row:

```html
<div class="space-y-1">
  <div class="flex gap-1">  <!-- 4 segments -->
    <span class="h-1.5 flex-1 rounded-full transition-colors duration-300
                 [filled: BAR[tone] | empty: bg-foreground/10]"></span> …
  </div>
  <div class="flex items-center justify-between gap-3">
    <span class="text-[11px] font-medium [TEXT[tone]]">{label}</span>
    <span class="truncate text-right text-[11px] text-muted-foreground">{hint}</span>
  </div>
</div>
```
Ramps (index by tone 0–4): bars `["bg-foreground/15","bg-red-500","bg-amber-500",
"bg-lime-500","bg-emerald-500"]`; text `["text-muted-foreground","text-red-600",
"text-amber-600","text-lime-600","text-emerald-600"]`. Scoring: min length 8;
points accrue for length tiers (≥8/≥12/≥16) and character-class diversity
(≥2/≥3/≥4 classes), capped at 1 for known-common/low-entropy passwords; labels
Weak/Fair/Good/Strong.

### 10.8 "or" divider

```html
<div class="flex items-center gap-3 text-[11px] uppercase tracking-wide
            text-muted-foreground">
  <span class="h-px flex-1 bg-border"></span> or <span class="h-px flex-1 bg-border"></span>
</div>
```

---

## 11. Cards & Surfaces

The shadcn `Card` family is the surface primitive:

| Part | Classes |
|---|---|
| `Card` | `rounded-lg border bg-card text-card-foreground shadow-sm` |
| `CardHeader` | `flex flex-col space-y-1.5 p-6` |
| `CardTitle` | `text-2xl font-semibold leading-none tracking-tight` (`<h3>`) |
| `CardDescription` | `text-sm text-muted-foreground` |
| `CardContent` | `p-6 pt-0` |
| `CardFooter` | `flex items-center p-6 pt-0` |

In practice, dense product surfaces use a tighter hand-rolled card:
`rounded-xl border border-border bg-card p-4 shadow-sm` (stat tiles, hub cards) or
`rounded-lg border border-border bg-card p-5 shadow-sm` (settings sections). A
**settings section** adds a header block (`text-sm font-semibold` title +
`text-xs text-muted-foreground` description) above a `space-y-4` body, and turns
its border `border-destructive/30` for "danger" zones.

---

## 12. Tables & Lists

A single table recipe is used app-wide:

```html
<div class="overflow-hidden rounded-lg border border-border bg-card shadow-sm">
  <div class="max-h-[…px] overflow-y-auto">
    <table class="w-full text-sm">
      <thead class="sticky top-0 z-10 bg-card">
        <tr class="border-b border-border text-[10.2px] uppercase tracking-wide
                   text-muted-foreground">
          <th class="w-12 px-4 py-2.5 text-left"> … </th>
          <th class="cursor-pointer select-none px-4 py-2.5 text-left"> … </th>
        </tr>
      </thead>
      <tbody>
        <tr class="group cursor-pointer transition-colors hover:bg-muted/40
                   [selected: bg-primary/5]">
          <td class="px-4 py-1.5"> … </td>
        </tr>
      </tbody>
    </table>
  </div>
</div>
```

Rules:
- **Header:** sticky, white bg, `text-[10.2px] uppercase tracking-wide
  text-muted-foreground`, a single bottom hairline, padding `px-4 py-2.5`.
- **Rows:** **no zebra striping.** Hover `hover:bg-muted/40`; selected
  `bg-primary/5`; drop-target `bg-primary/5 ring-2 ring-inset ring-primary/30`.
  Data cells are tighter than the header (`px-4 py-1.5`).
- **Sort:** only the active column shows a `ChevronUp`/`ChevronDown` (`h-3.5 w-3.5`)
  beside its label; inactive columns show nothing.
- **Responsive:** lower-priority columns hide via `hidden sm:table-cell` /
  `hidden md:table-cell`.
- **Type pills in cells:** a "category" pill uses `bg-primary/10 text-primary`; a
  neutral/extension pill uses `bg-secondary`; both `rounded-md px-2 py-1 text-xs`.
- **Bulk-actions bar** (appears on selection): animates open via a grid-rows
  transition (`grid-rows-[0fr]` → `grid-rows-[1fr]`, `transition-all duration-300`)
  into a `border-b border-border bg-primary/5 px-4 py-1.5` strip of small
  (`h-7 text-[12px]`) action buttons; the delete action is `bg-destructive
  text-white`.
- **Row leading graphic:** a fixed `h-8 w-8` tile (file thumbnail or colored icon)
  so names align; a small count/star badge can overlay its corner.

For non-tabular lists (activity feeds), use `divide-y divide-border/70` rows of
`flex items-center gap-3 py-2.5`, an avatar with a corner action badge, and a
muted timestamp.

---

## 13. Modals, Overlays & Dialogs

Two coexisting systems — standardize on one when porting.

### 13.1 Shared blur-backed `Overlay` (preferred)

```html
<!-- backdrop -->
<div class="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 p-4
            backdrop-blur-sm animate-in fade-in duration-150">
  <!-- panel -->
  <div class="w-full max-w-md overflow-hidden rounded-xl border border-border
              bg-card shadow-2xl animate-in zoom-in-95 fade-in duration-200">
    {children}
  </div>
</div>
```
Portaled to `<body>`; closes on Escape and outside-click. Backdrop `bg-black/40` +
`backdrop-blur-sm`, panel `shadow-2xl`, enters with `zoom-in-95 fade-in`.

### 13.2 Standalone modals (Ack/Applied/Share/2FA)

A lighter chrome: backdrop `fixed inset-0 z-50 flex items-center justify-center
bg-black/50 p-4` (**no blur, no entrance animation**); panel `rounded-xl border
border-border bg-card shadow-xl` at `max-w-sm` (success) or `max-w-md` (forms).

### 13.3 Dialog anatomy

- **Header:** `flex items-center justify-between border-b border-border px-5 py-3.5`
  — an optional lead icon + `text-sm font-semibold` title on the left, a close
  button (`text-muted-foreground hover:text-foreground`, `X h-4 w-4`) on the right.
  Rich modals use an `h-9 w-9 rounded-xl bg-primary/10` icon tile in the header.
- **Body:** `px-5 py-3`/`py-4`, `space-y-1.5`–`space-y-4`.
- **Footer:** `flex justify-end gap-2 border-t border-border px-5 py-3` — **Cancel
  on the left, primary/destructive confirm on the right**, both `h-9`. Confirm uses
  the primary recipe; destructive confirm swaps to `bg-destructive text-white`.

### 13.4 Confirmation dialog

A `flex items-start gap-3` body: when destructive, a lead `h-9 w-9 rounded-lg
bg-destructive/10 text-destructive` tile with an `AlertTriangle h-5 w-5`, then a
`text-sm font-semibold` title + `text-[13px] text-muted-foreground` message.

### 13.5 Success modal

Centered column: an `h-12 w-12 rounded-full bg-emerald-500/10` badge with a
`CheckCircle2 text-emerald-600`, a `text-base font-semibold` title, a
`text-[13px] text-muted-foreground` message, and a full-width `h-9` primary OK
button (auto-focused; Enter/Escape both dismiss).

### 13.6 Step indicator (wizards)

A row of numbered bubbles joined by rails. Bubble `flex h-5 w-5 items-center
justify-center rounded-full text-[10px] font-semibold`: completed `bg-primary
text-primary-foreground` (shows a `Check`), active `bg-primary/15 text-primary
ring-1 ring-primary/40`, upcoming `bg-muted text-muted-foreground`. Connectors are
`h-px` rules colored `bg-primary/50` once passed. (A larger onboarding variant uses
`h-11 w-11` bubbles with `border-2`, an active `ring-4 ring-primary/15`, and
`h-[3px]` rails.)

---

## 14. Menus & Tooltips

Radix-based, shadcn-style, portaled.

### 14.1 Dropdown / context menu content

```
bg-popover text-popover-foreground z-50 min-w-[8rem] overflow-y-auto rounded-md
border p-1 shadow-md
data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95
data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95
/* dropdowns add directional: data-[side=bottom]:slide-in-from-top-2, etc. */
```
Sub-menu content uses `shadow-lg`. Context menus omit the directional slides
(fade/zoom only).

### 14.2 Menu item

```
relative flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-sm
outline-none select-none focus:bg-accent focus:text-accent-foreground
data-[disabled]:pointer-events-none data-[disabled]:opacity-50
/* leading icons default to text-muted-foreground, size-4, → accent-foreground on focus */
```
- **Inset** items add `pl-8` (aligns text under a checkmark column).
- **Destructive** items: `text-destructive`, on focus `bg-destructive
  text-destructive-foreground`.
- **Separator:** `bg-border -mx-1 my-1 h-px`. **Label:** `px-2 py-1.5 text-sm
  font-medium`. **Shortcut:** `ml-auto text-xs tracking-widest text-muted-foreground`.

### 14.3 Tooltip

```
bg-foreground text-background z-50 w-fit rounded-md px-3 py-1.5 text-xs text-balance
animate-in fade-in-0 zoom-in-95 …
```
Inverted (dark bg / light text), tiny, with an optional 45°-rotated `size-2.5`
arrow (`bg-foreground fill-foreground rounded-[2px]`). Default delay **0**
(instant).

---

## 15. Banners & Info Strips

### 15.1 Info tip (single subtle variant)

```html
<div class="flex items-start gap-3 rounded-lg border border-primary/15
            bg-primary/[0.04] px-4 py-2.5 text-xs leading-relaxed text-foreground/80">
  <Icon class="mt-0.5 h-4 w-4 flex-shrink-0 text-primary/70" />
  <div class="min-w-0 flex-1">{children}</div>
  <!-- optional dismiss X: rounded p-1 text-muted-foreground/60 hover:bg-primary/10 -->
</div>
```
Default icon `Lightbulb`. Dismissals can persist to `localStorage`.

### 15.2 State banners

Build from the §2.4 state palette: `flex items-start gap-3 rounded-lg border p-3`
(or `p-4`) with the matching `border-*/30 bg-*/5` and a lead icon. Examples:
warning `border-amber-500/30 bg-amber-500/5` + `text-amber-600` icon; success
`border-emerald-500/30 bg-emerald-500/5`; error `border-destructive/30
bg-destructive/5`.

---

## 16. Badges, Chips & Status Pills

### 16.1 Status pill (canonical)

```
rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide
```
colored by semantic state — a `/15` (or `/10`) tint background + a `-700`/`-600`
text shade:

| State | Recipe |
|---|---|
| Active / success | `bg-emerald-500/15 text-emerald-700` |
| Inactive / error | `bg-red-500/15 text-red-600` |
| Off / warning | `bg-amber-500/15 text-amber-700` |
| Popular / "coming soon" | `bg-primary/10 text-primary` (or solid `bg-primary text-primary-foreground`) |

### 16.2 "Soon" tag (de-emphasized)

`rounded bg-secondary px-1.5 py-0.5 text-[9px] uppercase tracking-wide
text-muted-foreground` — note `rounded` (not full), `text-[9px]`, not bold.

### 16.3 Toggle/filter chips

```
inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1 text-xs font-medium
transition-colors [active: bg-primary text-primary-foreground]
[idle: bg-secondary text-foreground hover:bg-secondary/70]
```
with an `h-4 w-4` lead icon and a `tabular-nums` count.

### 16.4 Compliance/neutral badges

`inline-flex items-center gap-1.5 rounded-md border border-border bg-card px-2 py-1
text-[10px] font-semibold uppercase tracking-wide text-muted-foreground/70` (often
`grayscale` for logo marks).

---

## 17. Meters, Progress & Sparklines

### 17.1 Progress bar (universal)

```html
<div class="h-1.5 w-full overflow-hidden rounded-full bg-muted">       <!-- track -->
  <div class="h-full rounded-full transition-all" style="width: {pct}%"></div> <!-- fill -->
</div>
```
- Track is always `bg-muted`, `rounded-full`, `overflow-hidden`; height `h-1.5`
  (thin) or `h-2` (usage pages).
- **Fill color by threshold:** `bg-primary` normally → `bg-destructive/70` at
  ≥80% → `bg-destructive` at ≥100%. A warning tier uses `bg-amber-500`.
- **Unlimited** state: a gradient fill `bg-gradient-to-r from-primary/30
  via-primary to-primary/30` at full width.
- Enforce a **minimum visible width** (e.g. `max(pct, 6%)`) when the value is
  non-zero so a sliver always shows.

### 17.2 Seat/usage meter card

`rounded-xl border border-border bg-card px-4 py-3.5 shadow-sm` with a header line
(`flex flex-wrap items-center justify-between`: bold count + muted breakdown + a
threshold-colored status string), a small "add" button, and the bar below.

### 17.3 Sparkline

A lightweight inline SVG (`~120×34`, rendered `h-9 w-full`,
`preserveAspectRatio="none"`): a `strokeWidth={1.5}` rounded line
(`vectorEffect="non-scaling-stroke"`) over a vertical area gradient from
`stopOpacity 0.2` (top) to `0` (bottom) in the series color. Default stroke
`#635BFF`; per-metric accents (e.g. emerald, violet) can override.

---

## 18. Dashboard & Stat Cards

- **Shell:** `mx-auto max-w-6xl px-6 py-7 lg:px-8`; a greeting block (muted account
  line + `text-2xl font-semibold tracking-tight` title); a half-width search.
- **Grid:** `grid gap-5 lg:grid-cols-3` with a `lg:col-span-2` main column and a
  `space-y-4` right rail. Stat tiles in `grid grid-cols-1 gap-4 sm:grid-cols-2`
  (or `sm:grid-cols-3`).
- **Stat tile:** `flex flex-col rounded-xl border border-border bg-card p-4
  shadow-sm`; header row pairs a `text-[12px] font-medium text-muted-foreground`
  label with an `h-4 w-4 text-muted-foreground` icon; the **number** is
  `text-[22px] font-semibold leading-none tabular-nums text-foreground`; an
  optional sublabel (`text-[11px] text-muted-foreground`) and sparkline follow.
- **Promo/upgrade card:** `rounded-xl border border-primary/30 bg-primary/5 p-4
  shadow-sm hover:bg-primary/10` with an icon tile, title, subtext, and a trailing
  `ArrowRight text-primary`.
- **Span toggle (segmented):** `inline-flex items-center gap-0.5 rounded-lg border
  border-border bg-muted/40 p-0.5`; selected button `bg-secondary text-foreground
  shadow-sm`.

---

## 19. Empty States & Skeletons

### 19.1 Empty state

Centered column: `flex flex-col items-center text-center` (often `max-w-md`), a
hero icon tile (`h-16 w-16 rounded-2xl bg-primary/10 text-primary` with an
`h-8 w-8` icon), a `text-2xl`/`text-lg font-semibold text-foreground` heading, a
muted body (`text-[15px]`/`text-sm`), and an optional `h-11` CTA. Smaller inline
empties are just muted `text-[12px]`–`text-[12.5px]` with `py-6`/`mt-4`. Note tiles
use a **dashed** border: `rounded-lg border border-dashed border-border p-3`.

### 19.2 Skeleton

```
animate-pulse rounded-md bg-muted    /* callers add h-*/w-*/rounded-* per shape */
```
Mirror the real layout's structure (same card chrome, same block positions) so the
load-in is non-jarring. Inline pulses may use `bg-foreground/10` / `bg-foreground/5`
for lower contrast.

---

## 20. Avatars

Radix Avatar (shadcn):

| Part | Classes |
|---|---|
| Root | `relative flex size-8 shrink-0 overflow-hidden rounded-full` |
| Image | `aspect-square size-full` (`object-cover`) |
| Fallback | `flex size-full items-center justify-center rounded-full` |

Default fallback tint: `bg-primary/10 text-[11px] font-medium text-primary`
(initials). For a "ring", use a **real padded circle** (`flex rounded-full
bg-[color] p-[3.9px]`) rather than a `ring-` utility (see §5).

---

## 21. Authentication & Onboarding Screens

### 21.1 Two-column split (`AuthLayout`)

```html
<div class="flex min-h-screen bg-background">
  <!-- LEFT: form column -->
  <div class="flex w-full flex-col px-6 py-8 sm:px-10 lg:w-[44%] lg:px-16 xl:px-24">
    <div class="flex flex-1 flex-col justify-center py-10">
      <div class="mx-auto w-full max-w-[30.8rem]">
        <div class="rounded-2xl border border-border bg-card p-8 shadow-lg
                    shadow-primary/5 sm:p-10">
          <Logo class="mb-7 h-9 text-foreground" />
          {children}
        </div>
      </div>
    </div>
    <!-- single-line footer notice, centered on card axis -->
  </div>

  <!-- RIGHT: promo panel (hidden < lg) -->
  <div class="relative hidden overflow-hidden lg:flex lg:w-[56%]">
    <div class="absolute inset-0 bg-gradient-to-br from-primary
                to-[oklch(0.30_0.13_278)]"></div>
    <!-- decorative low-opacity white orbs -->
    <div class="relative z-10 flex w-full flex-col justify-center px-16 py-16
                text-primary-foreground xl:px-24">
      <p class="text-[13px] font-medium uppercase tracking-[0.2em]
                text-primary-foreground/70">{eyebrow}</p>
      <h2 class="mt-5 max-w-xl text-4xl font-semibold leading-[1.12]
                 xl:text-[2.75rem]">{headline}</h2>
      <p class="mt-5 max-w-md text-[15px] leading-relaxed
                text-primary-foreground/80">{subcopy}</p>
      <ul class="mt-12 space-y-6 max-w-md"> …feature items… </ul>
    </div>
  </div>
</div>
```

- The form column is full-width on mobile, **44%** at `lg`; the promo panel is
  **56%** and **hidden below `lg`**.
- The promo panel is an indigo→deep-purple diagonal gradient
  (`from-primary to-[oklch(0.30 0.13 278)]`) with three low-opacity white "orbs"
  (`bg-white/[0.04]`–`/[0.06]`) for depth, and a feature list whose items pair an
  `h-9 w-9 rounded-lg bg-white/15 ring-1 ring-white/20` icon badge with a
  title + body.
- The auth card is softer than product cards: `rounded-2xl`, `p-8 sm:p-10`,
  `shadow-lg shadow-primary/5`.

### 21.2 Auth form conventions

- Heading `text-2xl font-semibold text-foreground` + sub `mt-1 text-sm
  text-muted-foreground`.
- **OAuth buttons:** full-width `h-10` (or `py-2.5`) bordered buttons,
  `flex … items-center justify-center gap-2.5 rounded-md border border-foreground/25
  text-sm font-medium hover:bg-muted`, with a multi-color provider glyph
  (`h-[18px] w-[18px]`), followed by an "or" divider.
- **Submit:** `h-9 w-full` (prominent CTA `h-11`), label swaps to a "-ing…" form
  while busy.
- **Step transitions:** `duration-200 animate-in fade-in` plus a directional
  `slide-in-from-left-4` / `slide-in-from-right-4` to suggest forward/back motion.
- **Links:** brand actions `text-primary hover:underline` (often `font-medium`);
  secondary actions `text-muted-foreground hover:text-foreground`.

### 21.3 Account picker rows

`group flex w-full items-center gap-3 rounded-xl border border-border bg-card
px-4 py-3.5 text-left hover:border-primary/40 hover:bg-primary/[0.03]`, with a lead
`h-9 w-9 rounded-lg bg-primary/10 text-primary` icon tile, a name/subtitle stack,
and a trailing affordance (`ChevronRight` that nudges `group-hover:translate-x-0.5`,
or a `Check`/spinner for the active/busy one).

---

## 22. Motion & Animation

Powered by `tailwindcss-animate` (`animate-in`/`animate-out` + `fade`/`zoom`/`slide`
utilities), kept short and purposeful:

| Element | Motion |
|---|---|
| Overlay backdrop | `animate-in fade-in duration-150` |
| Overlay/modal panel | `animate-in zoom-in-95 fade-in duration-200` |
| Menu/tooltip content | open `fade-in-0 zoom-in-95` (+ directional `slide-in-from-*-2`); close `fade-out-0 zoom-out-95` |
| Auth step change | `duration-200 animate-in fade-in slide-in-from-left/right-4` |
| Sidebar collapse | `transition-[width] duration-300 ease-in-out` |
| Nav hover/active pill | `transition-opacity`/`transition-all duration-200` |
| Bulk-actions bar | grid-rows `0fr→1fr`, `transition-all duration-300 ease-out` |
| Progress/strength fills | `transition-all` / `transition-colors duration-300` |
| Spinners | `animate-spin` on `Loader2` |
| Skeletons | `animate-pulse` |

Durations cluster at **150–300ms**; easing defaults to Tailwind's standard, with
`ease-in-out` for the sidebar and `ease-out` for the actions bar.

---

## 23. Brand & Logo

- The logo is an **inline SVG wordmark** (vector letterforms), drawn with
  `fill="currentColor"` so a single `text-*` utility recolors it — `text-white` on
  the navy chrome bar (`h-[28px]`), `text-foreground` on the auth card (`h-9`),
  brand color on signup (`h-[34px]`).
- A **mark-only** square variant exists for collapsed sidebars / favicons.
- A script display font (`font-logo` → Lobster) is registered for an optional
  script wordmark but the production logo is pure SVG.

---

## 24. Porting Checklist

To apply this system to a new project:

1. **Copy the token block** (§2.1) into your global CSS `:root`, and wire each
   token in `tailwind.config` with the `oklch(var(--x) / <alpha-value>)` pattern
   (§2.3). Re-skin by editing only the OKLCH values — keep the *names*.
2. **Set `--radius`** (8px) and copy the radius + navy-tinted shadow scales (§4).
3. **Load Inter** (with the `cv02/cv03/cv04/cv11` features) and set the font stacks
   (§3.1).
4. **Decide on the desktop-zoom mechanic** (§5) — adopt it for density, or drop it;
   if you keep it, also keep the popper counter-zoom and the padded-circle "ring"
   rule.
5. **Install** `lucide-react`, `class-variance-authority`, `clsx`,
   `tailwind-merge` (the `cn()` helper), `tailwindcss-animate`, and the Radix
   primitives you need; standardize icon sizes per §6.
6. **Build the primitives** in this order: `Button` (CVA, §9) → `Input`/`Label`
   (§10) → `Card` (§11) → the size-locked `checkboxCls` + toggle (§10.4–10.5) →
   one `Overlay` (pick §13.1) → Radix dropdown/tooltip wrappers (§14).
7. **Compose the shell** (§7): navy chrome bar + collapsible sidebar (§8) + floating
   white content card, gated by a `ProtectedRoute`.
8. **Adopt the cross-cutting rules:** white cards on tinted canvas; active = inset
   pill + 3px bar; status = semantic tint + `-600/-700` text; numbers `tabular-nums`;
   uppercase-tracked-muted labels; one-primary-button-per-view; Cancel-left /
   Confirm-right in footers; no zebra tables; 150–300ms motion.

> **One sentence to hand a new designer:** *Cool indigo on near-white, white cards
> that float with hairline borders and navy-tinted shadows, compact controls, a
> navy top bar, a layered-pill active state, and semantic emerald/amber/red used
> only for state — never for brand.*
