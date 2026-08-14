# Trilli Design Spec — UI & React Conventions

This is the working design specification for the Trilli web app (`interface/`).
It documents the design language and the React conventions the codebase
actually follows, so new surfaces land looking and behaving like the rest of
the product. When in doubt, copy an existing pattern from Files or Trilli Sign
— consistency beats novelty.

---

## 1. Stack & architecture

| Layer | Choice |
|---|---|
| Framework | React 18 + TypeScript, built with Vite (`@vitejs/plugin-react-swc`) |
| Routing | `react-router-dom` v6 — all routes declared in `src/App.tsx` |
| Styling | Tailwind CSS with **oklch design tokens** (`src/index.css`), `tailwindcss-animate` |
| Primitives | Radix UI (`dropdown-menu`, `context-menu`, `tooltip`, `avatar`, `label`, `slot`) wrapped shadcn-style in `src/components/ui/` |
| Icons | `lucide-react` inline for actions/UI; **generated image assets** for app & tool identity icons (see §6) |
| Data | Thin typed fetch wrappers in `src/lib/api.ts` + per-domain modules (`lib/sign/`, `lib/pdf/`, `lib/productivity/`). TanStack Query is wired at the root but most pages use local state + explicit `reload()` |
| Delivery | `npm run build` → `dist/` → embedded into the Go binary via `//go:embed all:dist` (`system/web`). One binary serves API + SPA; deploy = `go build` + `systemctl restart trilli-serve` |

There is no client-side global store. Server state lives on the server; pages
fetch what they need and refetch after mutations.

## 2. Design language

Stripe-influenced: calm cool-gray canvas, white cards that "punch out", one
saturated brand color used sparingly, navy chrome.

### Color tokens (oklch CSS vars, `src/index.css`)

| Token | Hex anchor | Use |
|---|---|---|
| `--background` | `#f6f8fa` | Page canvas (cool near-white) |
| `--card` | `#ffffff` | Cards, tables, containers, popovers |
| `--primary` | `#635bff` | CTAs, active states, focus rings, links ("blurple") |
| `--secondary` | `#eef0ff` | Low-emphasis buttons/chips (soft indigo surface) |
| `--muted` / `--muted-foreground` | `#f1f4f8` / gray | Row hovers, ghost buttons, secondary text |
| `--border` | `#e3e8ee` | Hairline borders everywhere |
| `--destructive` | `#f13744` | Delete/void/decline states |
| `--chrome` | `#0a2540` | Global top bar, dark headers, brand navy blocks |
| `--success` | green | Completed states, positive chips |

Rules:
- **Never hardcode grays** — use tokens (`text-muted-foreground`, `border-border`, `bg-muted`).
- Feature accent colors (recipient colors, PDF tool colors) are defined once in
  their domain module (`lib/sign/api.ts` `RECIPIENT_COLORS`, `lib/pdf/tools.ts`)
  and derived with helpers (`tint()`, `muted()`, `mix()`) — pastels are mixed
  toward white, never made translucent over content.
- Brand navy `#0A2540` is used literally for app-identity blocks (Sign header
  icon, disclosure badges) and shadows are navy-tinted, not black.

### Typography

- Font: **Inter** (system fallbacks). `font-logo` (Lobster) only for the
  wordmark; `font-pdf` (Hanken Grotesk) only for Trilli PDF display text;
  "Great Vibes" only for signature script rendering.
- Sizes are **explicit bracket values**, tuned per role rather than the
  default scale. The working ramp:
  - `text-[9.5px]`–`text-[10px]` bold uppercase — chips, tiny badges
  - `text-[11px]`–`text-[11.5px]` — column headers (uppercase, `tracking-wide`), sublabels, form labels (`font-semibold text-muted-foreground`)
  - `text-[12px]`–`text-[12.5px]` — table cell secondary text, compact buttons
  - `text-[13px]`–`text-[13.5px]` — body, inputs, buttons, table primary text
  - `text-[14px]`–`text-[15px]` — dialog/section titles (`font-semibold`)
  - `text-lg`/`text-xl` — page titles only
- Numbers in tables get `tabular-nums`; timestamps are single-line
  (`whitespace-nowrap`), formatted `Jul 4, 2026 · 3:15 AM` (no seconds).

### Shape & elevation

- Radius: cards/containers `rounded-xl`, controls `rounded-lg` or `rounded-md`,
  compact action buttons `rounded-[5px]`, status chips `rounded` (squarer, not
  pills). Search inputs are the one pill-ish exception (`rounded-[11px]`).
- Elevation is subtle: `shadow-sm` on cards, `shadow-xl`/`shadow-2xl` only on
  overlays. Hairline `border border-border` does most separation work.
- **No hover motion.** Cards/tiles must not translate or scale on hover —
  hover feedback is background/border color only (`hover:bg-muted/40`,
  `hover:border-foreground/15`).

## 3. Recurring components & patterns

Reuse these instead of re-inventing:

- **`cn()`** (`lib/utils.ts`, clsx + tailwind-merge) for all conditional classes.
- **Tables**: `overflow-hidden rounded-xl border border-border bg-card shadow-sm`
  wrapping a `w-full text-left` table; header row `text-[11px] font-semibold
  uppercase tracking-wide text-muted-foreground border-b`; rows
  `border-b border-border/60 last:border-0 hover:bg-muted/40 cursor-pointer`;
  cell padding `px-5 py-3.5` (Files uses a denser `px-4 py-1.5`).
- **Row actions are icons**, not text buttons: `rounded p-1.5
  text-muted-foreground` with intent-colored hover (`hover:bg-primary/10
  hover:text-primary` for constructive, `hover:bg-destructive/10
  hover:text-destructive` for destructive), `h-4 w-4` lucide icon, `title`
  tooltip, `inline-flex items-center align-middle` so neighbors line up.
  Transient success feedback = swap the icon for a green `Check` ~2.5s.
- **Status chips**: `rounded px-1.5 py-px text-[9.5px] font-bold uppercase
  tracking-wide` + intent tint (`bg-primary/10 text-primary`, etc.). See
  `StatusChip` in `pages/sign/SignHome.tsx`.
- **Paginator**: shared `components/Paginator.tsx` ("1 of N" + numbered pages
  + chevrons). `bordered` prop: on inside a table card, off standalone.
- **Search inputs**: right-docked above tables — `h-[34px] rounded-[11px]
  border-border bg-card pl-10 pr-9 text-[13.5px]` with a left `Search` icon
  and a clear-✕ button; filter live, client-side, across every visible column;
  reset to page 1 on change.
- **Dialogs** (`components/Dialogs.tsx`): portal `Overlay` (black/40 backdrop
  + blur, `animate-in fade-in zoom-in-95`), `max-w-md rounded-xl` card,
  content `px-5 pt-4`, footer `border-t px-5 py-3` with **compact
  right-aligned buttons** (`h-8 text-[12.5px]`, Cancel outlined + primary/red
  action). Use `ConfirmDialog` for every destructive confirmation — never
  `window.confirm`. Keep copy terse: title question + the object's name.
- **Destination browser**: `MoveItemsModal` (`components/SelectionActionsModal.tsx`)
  is the standard workspace/folder chooser; it's generalized via
  `title/subtitle/actionPrefix/allowNoop/iconKind` props.
- **Dropdown menus**: prefer Radix `DropdownMenu`; for tiny hand-rolled menus
  use a fixed inset-0 click-away layer + `rounded-xl border bg-card py-1.5
  shadow-xl ring-1 ring-black/5`, items `px-3.5 py-2 text-[13px]`, divider
  before destructive items. Never let a parent's `overflow-hidden` clip a
  menu — drop it below/above the trigger with `z-50`.
- **Skeletons**: shimmering placeholders while heavy content renders (see
  `SkeletonPage` in `SignEditor.tsx` for the document-page pattern:
  `animate-pulse` bars in an aspect-ratio box, swap on `onLoad`).
- **Empty states**: centered in a bordered card — icon, one-line heading,
  short body, single primary CTA. On a truly empty index, hide redundant
  header CTAs and lead with the one empty-state button.
- **Accordion sections** (setup flows): `AccordionSection` — `border-b` block,
  full-width toggle row with title + rotating `ChevronDown`.
- **System/protected notices**: lock badge on navy square + bold one-liner +
  muted explanation, in a `rounded-xl border bg-muted/30` band. Same copy at
  every depth of a protected tree; enforce server-side too, never chrome-only.

## 4. React conventions

- **Pages own their data.** Fetch via the typed `lib/api.ts` client (or the
  domain module: `signApi`, pdf tools, productivity), keep it in `useState`,
  and expose a `reload()` used after every mutation. Errors land in a local
  `error` string rendered as a destructive callout near the top.
- **Optimistic where it matters** (drag/resize field placement), pessimistic
  + `reload()` everywhere else.
- **URL is state**: wizard steps (`?step=fields`), workspace/folder location
  (`/files/w/<token>/f/<token>`). Numeric DB ids are **never** shown in URLs —
  encode with `lib/ids.ts` (`encodeId`/`decodeId`, Optimus-style bijection).
  JSON APIs still speak integers; only the address bar is opaque.
- **Remount on tenant switch**: key tenant-scoped subtrees by tenant id so a
  workspace change resets navigation state instead of leaking it.
- **Identity/gating**: `contexts/AuthContext` provides `identity`;
  feature access helpers live in `lib/productivity/access.ts`. Gate in the
  route/stub layer, and mirror every client gate with a server-side check.
- **Drag & drop**: use **window-level** listeners with an enter/leave depth
  counter for page-wide file-drop detection (highlight the drop target, not
  the page); `stopPropagation` on inner zones; always `preventDefault` on
  `dragover` so the browser never navigates to a dropped file.
- **Keyboard**: Escape closes overlays; arrow-nudge/Delete handlers skip when
  focus is in an input/textarea/select.
- **Portals** (`createPortal` to `document.body`) for anything that must
  escape stacking contexts.
- **Comments** explain constraints, not mechanics — see existing files for
  tone (`// NOTE: no reload() here — refetching flips status…`).

## 5. Layout anatomy

- Global chrome: navy top bar; collapsible `components/Sidebar.tsx` (MAIN /
  APPS / MANAGE groups, storage meter, trash badge).
- Page shell: `mx-auto max-w-7xl px-6 py-8 lg:px-8`, `space-y` rhythm.
  **Inside flex parents, a centered content div needs `w-full` alongside
  `mx-auto max-w-*`** or it shrinks to content width.
- Page header: identity icon on a navy rounded square + title + one-line
  muted description, primary CTA on the right.
- Full-screen flows (wizards, editors) use their own shell (`WizardShell`)
  with a slim header: close ✕ / back, title, actions right.

## 6. Iconography & generated assets

Two tiers:

1. **UI icons** — lucide-react inline, sized `h-3.5`–`h-5`, colored by tokens.
2. **Identity icons** (suite apps, PDF tools) — **generated image assets** in
   the Fluent-style layered language: gradient badge plane floating over a
   white document plane, per-item color triads (`dark/mid/light` mixed from a
   brand hex), navy cast shadows, rendered SVG → headless Chromium →
   transparent 512px PNG. Master artwork lives in generator scripts — **edit
   the generator and re-render; never hand-edit the PNGs.**
   - PDF tools: `interface/scripts/pdficons/` → `interface/public/img/pdf-tools/*.png`,
     served by `PdfToolIcon` (`lib/pdf/icons.tsx`).
   - Marketing suite icons: same pipeline in the trilli-site repo.

## 7. Verification & shipping

- Type-check + build before calling anything done:
  `npx tsc --noEmit && npm run build` (watch for `built in`), then
  `go build -o bin/trilli-serve ./cmd/trilli && sudo systemctl restart trilli-serve`.
- Verify UI changes **in the live app** (headless Playwright with a minted
  session; screenshot the actual pixels — measure when alignment matters).
- Commit to main after each finished unit of work; never commit `dist/` diffs
  without their source changes.
