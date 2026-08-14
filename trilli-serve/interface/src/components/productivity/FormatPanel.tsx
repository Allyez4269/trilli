// TrilliDocs Format panel — the right-hand properties deck opened by the toolbar's
// side-menu button. A roomier, Trilli-styled surface for the current selection's
// Text / Paragraph / Style formatting, driving the headless engine via the same
// UNO commands as the toolbar, with live active-state from CommandStateChanged.
import {
  AlignCenter,
  AlignJustify,
  AlignLeft,
  AlignRight,
  Baseline,
  Bold,
  Highlighter,
  IndentDecrease,
  IndentIncrease,
  Italic,
  List,
  ListOrdered,
  Minus,
  Plus,
  RemoveFormatting,
  Strikethrough,
  Subscript,
  Superscript,
  Underline as UnderlineIcon,
  X,
} from "lucide-react";

import {
  COLOR_CMD,
  FONT_NAME_CMD,
  FONT_SIZE_CMD,
  FONTS,
  FONT_SIZES,
  HIGHLIGHT_CMD,
  STYLE_CMD,
  STYLE_GALLERY,
  UNO,
  colorArgs,
  fontNameArgs,
  fontSizeArgs,
  hexToRgbInt,
  highlightArgs,
  isActive,
  styleArgs,
} from "@/lib/productivity/uno";
import { cn } from "@/lib/utils";

type Exec = (command: string, args?: Record<string, unknown>) => void;
type LucideIcon = typeof Bold;

function Btn({
  icon: Icon,
  title,
  active,
  onClick,
}: {
  icon: LucideIcon;
  title: string;
  active?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      title={title}
      aria-pressed={active}
      onMouseDown={(e) => e.preventDefault()}
      onClick={onClick}
      className={cn(
        "flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-md transition-colors",
        active ? "bg-primary/15 text-primary" : "text-foreground hover:bg-foreground/10",
      )}
    >
      <Icon className="h-4 w-4" />
    </button>
  );
}

function ColorBtn({ icon: Icon, title, onPick }: { icon: LucideIcon; title: string; onPick: (hex: string) => void }) {
  return (
    <label
      title={title}
      onMouseDown={(e) => e.preventDefault()}
      className="flex h-8 w-8 flex-shrink-0 cursor-pointer items-center justify-center rounded-md text-foreground hover:bg-foreground/10"
    >
      <Icon className="h-4 w-4" />
      <input type="color" className="sr-only" onChange={(e) => onPick(e.target.value)} />
    </label>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="space-y-2">
      <h3 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">{title}</h3>
      {children}
    </div>
  );
}

const selectCls =
  "h-8 rounded-md border border-border bg-background px-2 text-[12.5px] text-foreground outline-none focus-visible:ring-1 focus-visible:ring-primary/40";

export function FormatPanel({
  exec,
  states,
  onClose,
}: {
  exec: Exec;
  states: Record<string, string>;
  onClose: () => void;
}) {
  return (
    <aside className="flex h-full w-64 flex-shrink-0 flex-col border-l border-border bg-card">
      <header className="flex h-11 flex-shrink-0 items-center justify-between border-b border-border px-3">
        <span className="text-[13px] font-semibold text-foreground">Format</span>
        <button
          type="button"
          title="Close"
          onClick={onClose}
          className="flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        >
          <X className="h-4 w-4" />
        </button>
      </header>

      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto p-3">
        <Section title="Text">
          <select
            className={cn(selectCls, "w-full")}
            defaultValue="Calibri"
            title="Font"
            onChange={(e) => exec(FONT_NAME_CMD, fontNameArgs(e.target.value))}
          >
            {FONTS.map((f) => (
              <option key={f} value={f}>
                {f}
              </option>
            ))}
          </select>
          <div className="flex items-center gap-1">
            <Btn icon={Minus} title="Decrease size" onClick={() => exec(UNO.Shrink)} />
            <select
              className={cn(selectCls, "w-16 text-center")}
              defaultValue="11"
              title="Font size"
              onChange={(e) => exec(FONT_SIZE_CMD, fontSizeArgs(Number(e.target.value)))}
            >
              {FONT_SIZES.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
            <Btn icon={Plus} title="Increase size" onClick={() => exec(UNO.Grow)} />
          </div>
          <div className="flex flex-wrap items-center gap-1">
            <Btn icon={Bold} title="Bold" active={isActive(states, UNO.Bold)} onClick={() => exec(UNO.Bold)} />
            <Btn icon={Italic} title="Italic" active={isActive(states, UNO.Italic)} onClick={() => exec(UNO.Italic)} />
            <Btn icon={UnderlineIcon} title="Underline" active={isActive(states, UNO.Underline)} onClick={() => exec(UNO.Underline)} />
            <Btn icon={Strikethrough} title="Strikethrough" active={isActive(states, UNO.Strike)} onClick={() => exec(UNO.Strike)} />
            <Btn icon={Subscript} title="Subscript" active={isActive(states, UNO.Sub)} onClick={() => exec(UNO.Sub)} />
            <Btn icon={Superscript} title="Superscript" active={isActive(states, UNO.Super)} onClick={() => exec(UNO.Super)} />
            <ColorBtn icon={Baseline} title="Text color" onPick={(h) => exec(COLOR_CMD, colorArgs(hexToRgbInt(h)))} />
            <ColorBtn icon={Highlighter} title="Highlight color" onPick={(h) => exec(HIGHLIGHT_CMD, highlightArgs(hexToRgbInt(h)))} />
            <Btn icon={RemoveFormatting} title="Clear formatting" onClick={() => exec(UNO.ClearFormat)} />
          </div>
        </Section>

        <Section title="Paragraph">
          <div className="flex items-center gap-1">
            <Btn icon={AlignLeft} title="Align left" active={isActive(states, UNO.Left)} onClick={() => exec(UNO.Left)} />
            <Btn icon={AlignCenter} title="Align center" active={isActive(states, UNO.Center)} onClick={() => exec(UNO.Center)} />
            <Btn icon={AlignRight} title="Align right" active={isActive(states, UNO.Right)} onClick={() => exec(UNO.Right)} />
            <Btn icon={AlignJustify} title="Justify" active={isActive(states, UNO.Justify)} onClick={() => exec(UNO.Justify)} />
          </div>
          <div className="flex items-center gap-1">
            <Btn icon={List} title="Bulleted list" active={isActive(states, UNO.Bullet)} onClick={() => exec(UNO.Bullet)} />
            <Btn icon={ListOrdered} title="Numbered list" active={isActive(states, UNO.Number)} onClick={() => exec(UNO.Number)} />
            <Btn icon={IndentDecrease} title="Decrease indent" onClick={() => exec(UNO.IndentDec)} />
            <Btn icon={IndentIncrease} title="Increase indent" onClick={() => exec(UNO.IndentInc)} />
          </div>
          <select
            className={cn(selectCls, "w-full")}
            defaultValue=""
            title="Line spacing"
            onChange={(e) => {
              if (e.target.value) exec(e.target.value);
              e.target.value = "";
            }}
          >
            <option value="" disabled>
              Line spacing
            </option>
            <option value={UNO.Spacing1}>Single</option>
            <option value={UNO.Spacing15}>1.5</option>
            <option value={UNO.Spacing2}>Double</option>
          </select>
        </Section>

        <Section title="Styles">
          <div className="grid grid-cols-1 gap-1">
            {STYLE_GALLERY.map((s) => (
              <button
                key={s.value}
                type="button"
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => exec(STYLE_CMD, styleArgs(s.value))}
                className="flex h-9 items-center rounded-md border border-border bg-background px-3 text-left transition-colors hover:border-primary/50 hover:bg-primary/5"
              >
                <span className={cn("truncate", s.preview)}>{s.label}</span>
              </button>
            ))}
          </div>
        </Section>
      </div>
    </aside>
  );
}
