// TrilliSlides toolbar — the Impress counterpart of DocsToolbar/SheetsToolbar: a
// single compact rounded bar driving the headless Impress engine via UNO, with
// live active-state from CommandStateChanged. Mirrors SheetsToolbar's helpers
// exactly; only the controls (slide ops, presentation-style text formatting,
// insert shapes/text-box, Present) differ.
//
// NOTE: laid out to the STANDARD presentation pattern — the user's reference
// toolbar image hadn't arrived when this was built, so the exact button set is
// adjustable once it does.
import {
  AlignCenter,
  AlignJustify,
  AlignLeft,
  AlignRight,
  AlignVerticalJustifyCenter,
  Baseline,
  Bold,
  ChevronDown,
  Highlighter,
  Image as ImageIcon,
  IndentDecrease,
  IndentIncrease,
  Italic,
  Layout,
  Link as LinkIcon,
  List,
  ListOrdered,
  Minus,
  Paintbrush,
  Play,
  Plus,
  Printer,
  Redo2,
  Shapes,
  Strikethrough,
  Table as TableIcon,
  Type,
  Underline as UnderlineIcon,
  Undo2,
  ZoomIn,
  ZoomOut,
} from "lucide-react";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  BASIC_SHAPES,
  COLOR_CMD,
  FONT_SIZE_CMD,
  FONT_SIZES,
  HIGHLIGHT_CMD,
  LINE_SPACINGS,
  SLIDE_LAYOUTS,
  SLIDES_UNO,
  colorArgs,
  fontSizeArgs,
  hexToRgbInt,
  highlightArgs,
  isActive,
  layoutArgs,
  shapeCmd,
} from "@/lib/productivity/slides-uno";
import { cn } from "@/lib/utils";

type Exec = (command: string, args?: Record<string, unknown>) => void;
type LucideIcon = typeof Bold;

function ToolBtn({
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
      onMouseDown={(e) => e.preventDefault() /* keep the engine's selection */}
      onClick={onClick}
      className={cn(
        "flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-md transition-colors",
        active ? "bg-primary/15 text-primary" : "text-foreground hover:bg-foreground/10",
      )}
    >
      <Icon className="h-[17px] w-[17px]" />
    </button>
  );
}

function ColorBtn({ icon: Icon, title, onPick }: { icon: LucideIcon; title: string; onPick: (hex: string) => void }) {
  return (
    <label
      title={title}
      onMouseDown={(e) => e.preventDefault()}
      className="flex h-7 w-7 flex-shrink-0 cursor-pointer items-center justify-center rounded-md text-foreground hover:bg-foreground/10"
    >
      <Icon className="h-[17px] w-[17px]" />
      <input type="color" className="sr-only" onChange={(e) => onPick(e.target.value)} />
    </label>
  );
}

function Sep() {
  return <span className="mx-1 h-5 w-px flex-shrink-0 bg-border" />;
}

const selCls =
  "h-7 flex-shrink-0 rounded-md bg-transparent px-1.5 text-[13px] text-foreground outline-none transition-colors hover:bg-foreground/10 cursor-pointer";

function IconMenu({
  icon: Icon,
  title,
  active,
  width,
  children,
}: {
  icon: LucideIcon;
  title: string;
  active?: boolean;
  width?: string;
  children: React.ReactNode;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          title={title}
          aria-pressed={active}
          onMouseDown={(e) => e.preventDefault()}
          className={cn(
            "flex h-7 flex-shrink-0 items-center gap-0.5 rounded-md px-1 transition-colors",
            active ? "bg-primary/15 text-primary" : "text-foreground hover:bg-foreground/10",
          )}
        >
          <Icon className="h-[17px] w-[17px]" />
          <ChevronDown className="h-3 w-3 flex-shrink-0 opacity-60" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className={cn("z-[120]", width ?? "w-52")}>
        {children}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function MenuRow({
  icon: Icon,
  label,
  active,
  onClick,
}: {
  icon?: LucideIcon;
  label: string;
  active?: boolean;
  onClick: () => void;
}) {
  return (
    <DropdownMenuItem onSelect={() => onClick()} className={cn("gap-2 text-[13px]", active && "text-primary")}>
      {Icon ? <Icon className="h-4 w-4 text-muted-foreground" /> : <span className="w-4" />}
      {label}
    </DropdownMenuItem>
  );
}

export function SlidesToolbar({
  exec,
  states,
  onInsertImage,
  onZoom,
  onPrint,
  onPresent,
}: {
  exec: Exec;
  states: Record<string, string>;
  onInsertImage: () => void;
  onZoom: (dir: 1 | -1) => void;
  onPrint: () => void;
  onPresent: () => void;
}) {
  return (
    <div className="flex h-12 flex-shrink-0 items-center border-b border-border bg-card px-3">
      <div className="flex h-9 w-full items-center gap-0.5 overflow-x-auto rounded-full bg-muted px-2.5 [-ms-overflow-style:none] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
        {/* history + print + format painter */}
        <ToolBtn icon={Undo2} title="Undo" onClick={() => exec(SLIDES_UNO.Undo)} />
        <ToolBtn icon={Redo2} title="Redo" onClick={() => exec(SLIDES_UNO.Redo)} />
        <ToolBtn icon={Printer} title="Print" onClick={onPrint} />
        <ToolBtn
          icon={Paintbrush}
          title="Clone formatting"
          active={isActive(states, SLIDES_UNO.FormatPaint)}
          onClick={() => exec(SLIDES_UNO.FormatPaint)}
        />
        <Sep />

        {/* zoom (stepwise) */}
        <ToolBtn icon={ZoomOut} title="Zoom out" onClick={() => onZoom(-1)} />
        <ToolBtn icon={ZoomIn} title="Zoom in" onClick={() => onZoom(1)} />
        <Sep />

        {/* slides: new slide + layout */}
        <ToolBtn icon={Plus} title="New slide" onClick={() => exec(SLIDES_UNO.NewSlide)} />
        <IconMenu icon={Layout} title="Slide layout" width="w-48">
          {SLIDE_LAYOUTS.map((l) => (
            <MenuRow key={l.value} label={l.label} onClick={() => exec(SLIDES_UNO.Layout, layoutArgs(l.value))} />
          ))}
          <DropdownMenuSeparator />
          <MenuRow label="More layouts…" onClick={() => exec(SLIDES_UNO.LayoutDialog)} />
        </IconMenu>
        <Sep />

        {/* font size: − [size] + */}
        <ToolBtn icon={Minus} title="Decrease font size" onClick={() => exec(SLIDES_UNO.Shrink)} />
        <select
          className={cn(selCls, "w-[56px] text-center")}
          title="Font size"
          defaultValue="18"
          onChange={(e) => exec(FONT_SIZE_CMD, fontSizeArgs(Number(e.target.value)))}
        >
          {FONT_SIZES.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        <ToolBtn icon={Plus} title="Increase font size" onClick={() => exec(SLIDES_UNO.Grow)} />
        <Sep />

        {/* character formatting + colors */}
        <ToolBtn icon={Bold} title="Bold" active={isActive(states, SLIDES_UNO.Bold)} onClick={() => exec(SLIDES_UNO.Bold)} />
        <ToolBtn icon={Italic} title="Italic" active={isActive(states, SLIDES_UNO.Italic)} onClick={() => exec(SLIDES_UNO.Italic)} />
        <ToolBtn
          icon={UnderlineIcon}
          title="Underline"
          active={isActive(states, SLIDES_UNO.Underline)}
          onClick={() => exec(SLIDES_UNO.Underline)}
        />
        <ToolBtn
          icon={Strikethrough}
          title="Strikethrough"
          active={isActive(states, SLIDES_UNO.Strike)}
          onClick={() => exec(SLIDES_UNO.Strike)}
        />
        <ColorBtn icon={Baseline} title="Text color" onPick={(h) => exec(COLOR_CMD, colorArgs(hexToRgbInt(h)))} />
        <ColorBtn icon={Highlighter} title="Highlight color" onPick={(h) => exec(HIGHLIGHT_CMD, highlightArgs(hexToRgbInt(h)))} />
        <Sep />

        {/* alignment */}
        <IconMenu
          icon={AlignLeft}
          title="Align"
          active={
            isActive(states, SLIDES_UNO.AlignLeft) ||
            isActive(states, SLIDES_UNO.AlignCenter) ||
            isActive(states, SLIDES_UNO.AlignRight) ||
            isActive(states, SLIDES_UNO.AlignJustify)
          }
        >
          <MenuRow icon={AlignLeft} label="Left" active={isActive(states, SLIDES_UNO.AlignLeft)} onClick={() => exec(SLIDES_UNO.AlignLeft)} />
          <MenuRow icon={AlignCenter} label="Center" active={isActive(states, SLIDES_UNO.AlignCenter)} onClick={() => exec(SLIDES_UNO.AlignCenter)} />
          <MenuRow icon={AlignRight} label="Right" active={isActive(states, SLIDES_UNO.AlignRight)} onClick={() => exec(SLIDES_UNO.AlignRight)} />
          <MenuRow icon={AlignJustify} label="Justify" active={isActive(states, SLIDES_UNO.AlignJustify)} onClick={() => exec(SLIDES_UNO.AlignJustify)} />
        </IconMenu>
        <Sep />

        {/* lists + indent */}
        <ToolBtn icon={List} title="Bulleted list" active={isActive(states, SLIDES_UNO.Bullet)} onClick={() => exec(SLIDES_UNO.Bullet)} />
        <ToolBtn icon={ListOrdered} title="Numbered list" active={isActive(states, SLIDES_UNO.Number)} onClick={() => exec(SLIDES_UNO.Number)} />
        <ToolBtn icon={IndentDecrease} title="Decrease indent" onClick={() => exec(SLIDES_UNO.IndentDec)} />
        <ToolBtn icon={IndentIncrease} title="Increase indent" onClick={() => exec(SLIDES_UNO.IndentInc)} />
        <IconMenu icon={AlignVerticalJustifyCenter} title="Line spacing" width="w-44">
          {LINE_SPACINGS.map((ls) => (
            <MenuRow key={ls.label} label={ls.label} onClick={() => exec(ls.cmd)} />
          ))}
        </IconMenu>
        <Sep />

        {/* insert */}
        <ToolBtn icon={Type} title="Insert text box" onClick={() => exec(SLIDES_UNO.TextBox)} />
        <ToolBtn icon={ImageIcon} title="Insert image" onClick={onInsertImage} />
        <IconMenu icon={Shapes} title="Insert shape" width="w-48">
          {BASIC_SHAPES.map((sh) => (
            <MenuRow key={sh.name} label={sh.label} onClick={() => exec(shapeCmd(sh.name))} />
          ))}
        </IconMenu>
        <ToolBtn icon={Minus} title="Insert line" onClick={() => exec(SLIDES_UNO.Line)} />
        <ToolBtn icon={TableIcon} title="Insert table" onClick={() => exec(SLIDES_UNO.Table)} />
        <ToolBtn icon={LinkIcon} title="Insert link" onClick={() => exec(SLIDES_UNO.Link)} />
        <Sep />

        {/* present — fullscreen slideshow (Esc to exit) */}
        <ToolBtn icon={Play} title="Present (fullscreen — Esc to exit)" onClick={onPresent} />
      </div>
    </div>
  );
}
