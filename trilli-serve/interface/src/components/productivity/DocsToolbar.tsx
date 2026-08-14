// TrilliDocs toolbar — Google-Docs-style: a single compact rounded bar of
// Trilli-branded controls driving the headless Writer engine via UNO commands,
// with live active-state from CommandStateChanged and an overflow "More" menu
// for less-common actions. Insert/find/spelling invoke the engine's own dialogs
// for now; P3-G6 replaces those with native React dialogs.
import {
  AlignCenter,
  AlignJustify,
  AlignLeft,
  AlignRight,
  Baseline,
  Bold,
  ChevronDown,
  GitCompare,
  Highlighter,
  Image as ImageIcon,
  IndentDecrease,
  IndentIncrease,
  Italic,
  Link as LinkIcon,
  List,
  ListOrdered,
  MessageSquare,
  Minus,
  MoreVertical,
  PanelRight,
  Paintbrush,
  Pilcrow,
  Plus,
  Printer,
  Redo2,
  RemoveFormatting,
  Search,
  SeparatorHorizontal,
  Sigma,
  SpellCheck,
  Strikethrough,
  Subscript,
  Superscript,
  Table as TableIcon,
  Underline as UnderlineIcon,
  Undo2,
  WholeWord,
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
      onMouseDown={(e) => e.preventDefault() /* keep the editor's selection */}
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

// Borderless inline dropdown that fits the pill (Google-style). Uncontrolled
// with a sensible default; applies on change.
const selCls =
  "h-7 flex-shrink-0 rounded-md bg-transparent px-1.5 text-[13px] text-foreground outline-none transition-colors hover:bg-foreground/10 cursor-pointer";;

export function DocsToolbar({
  exec,
  states,
  onTogglePanel,
  panelOpen,
  onInsertImage,
  onZoom,
  onPrint,
}: {
  exec: Exec;
  states: Record<string, string>;
  onTogglePanel: () => void;
  panelOpen: boolean;
  onInsertImage: () => void;
  onZoom: (dir: 1 | -1) => void;
  onPrint: () => void;
}) {
  return (
    <div className="flex h-12 flex-shrink-0 items-center border-b border-border bg-card px-3">
      <div className="flex h-9 w-full items-center gap-0.5 overflow-x-auto rounded-full bg-muted px-2.5 [-ms-overflow-style:none] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
        {/* history + print + format painter */}
        <ToolBtn icon={Undo2} title="Undo" onClick={() => exec(UNO.Undo)} />
        <ToolBtn icon={Redo2} title="Redo" onClick={() => exec(UNO.Redo)} />
        <ToolBtn icon={Printer} title="Print" onClick={onPrint} />
        <ToolBtn
          icon={Paintbrush}
          title="Clone formatting"
          active={isActive(states, UNO.FormatPaint)}
          onClick={() => exec(UNO.FormatPaint)}
        />
        <Sep />

        {/* paragraph style gallery */}
        <StyleMenu exec={exec} />
        <Sep />

        {/* font family */}
        <select
          className={cn(selCls, "w-[160px]")}
          title="Font"
          defaultValue="Calibri"
          onChange={(e) => exec(FONT_NAME_CMD, fontNameArgs(e.target.value))}
        >
          {FONTS.map((f) => (
            <option key={f} value={f}>
              {f}
            </option>
          ))}
        </select>
        <Sep />

        {/* font size: − [size] + */}
        <ToolBtn icon={Minus} title="Decrease font size" onClick={() => exec(UNO.Shrink)} />
        <select
          className={cn(selCls, "w-[56px] text-center")}
          title="Font size"
          defaultValue="11"
          onChange={(e) => exec(FONT_SIZE_CMD, fontSizeArgs(Number(e.target.value)))}
        >
          {FONT_SIZES.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        <ToolBtn icon={Plus} title="Increase font size" onClick={() => exec(UNO.Grow)} />
        <Sep />

        {/* character formatting */}
        <ToolBtn icon={Bold} title="Bold" active={isActive(states, UNO.Bold)} onClick={() => exec(UNO.Bold)} />
        <ToolBtn icon={Italic} title="Italic" active={isActive(states, UNO.Italic)} onClick={() => exec(UNO.Italic)} />
        <ToolBtn
          icon={UnderlineIcon}
          title="Underline"
          active={isActive(states, UNO.Underline)}
          onClick={() => exec(UNO.Underline)}
        />
        <ToolBtn icon={Strikethrough} title="Strikethrough" active={isActive(states, UNO.Strike)} onClick={() => exec(UNO.Strike)} />
        <ToolBtn icon={Subscript} title="Subscript" active={isActive(states, UNO.Sub)} onClick={() => exec(UNO.Sub)} />
        <ToolBtn icon={Superscript} title="Superscript" active={isActive(states, UNO.Super)} onClick={() => exec(UNO.Super)} />
        <ColorBtn icon={Baseline} title="Text color" onPick={(h) => exec(COLOR_CMD, colorArgs(hexToRgbInt(h)))} />
        <ColorBtn
          icon={Highlighter}
          title="Highlight color"
          onPick={(h) => exec(HIGHLIGHT_CMD, highlightArgs(hexToRgbInt(h)))}
        />
        <Sep />

        {/* insert: link, comment, image */}
        <ToolBtn icon={LinkIcon} title="Insert link" onClick={() => exec(UNO.Link)} />
        <ToolBtn icon={MessageSquare} title="Insert comment" onClick={() => exec(UNO.Comment)} />
        <ToolBtn icon={ImageIcon} title="Insert image" onClick={onInsertImage} />
        <Sep />

        {/* alignment */}
        <ToolBtn icon={AlignLeft} title="Align left" active={isActive(states, UNO.Left)} onClick={() => exec(UNO.Left)} />
        <ToolBtn
          icon={AlignCenter}
          title="Align center"
          active={isActive(states, UNO.Center)}
          onClick={() => exec(UNO.Center)}
        />
        <ToolBtn icon={AlignRight} title="Align right" active={isActive(states, UNO.Right)} onClick={() => exec(UNO.Right)} />
        <ToolBtn
          icon={AlignJustify}
          title="Justify"
          active={isActive(states, UNO.Justify)}
          onClick={() => exec(UNO.Justify)}
        />
        <Sep />

        {/* lists + indent */}
        <ToolBtn icon={List} title="Bulleted list" active={isActive(states, UNO.Bullet)} onClick={() => exec(UNO.Bullet)} />
        <ToolBtn
          icon={ListOrdered}
          title="Numbered list"
          active={isActive(states, UNO.Number)}
          onClick={() => exec(UNO.Number)}
        />
        <ToolBtn icon={IndentDecrease} title="Decrease indent" onClick={() => exec(UNO.IndentDec)} />
        <ToolBtn icon={IndentIncrease} title="Increase indent" onClick={() => exec(UNO.IndentInc)} />
        <ToolBtn icon={RemoveFormatting} title="Clear formatting" onClick={() => exec(UNO.ClearFormat)} />
        <Sep />

        {/* zoom (stepwise) */}
        <ToolBtn icon={ZoomOut} title="Zoom out" onClick={() => onZoom(-1)} />
        <ToolBtn icon={ZoomIn} title="Zoom in" onClick={() => onZoom(1)} />
        <Sep />

        {/* overflow: everything else */}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              title="More"
              className="flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-md text-foreground transition-colors hover:bg-foreground/10"
            >
              <MoreVertical className="h-[17px] w-[17px]" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="z-[120] w-52">
            <MenuRow label="Line spacing: 1.0" onClick={() => exec(UNO.Spacing1)} />
            <MenuRow label="Line spacing: 1.5" onClick={() => exec(UNO.Spacing15)} />
            <MenuRow label="Line spacing: 2.0" onClick={() => exec(UNO.Spacing2)} />
            <DropdownMenuSeparator />
            <MenuRow icon={TableIcon} label="Insert table" onClick={() => exec(UNO.Table)} />
            <MenuRow icon={Sigma} label="Special character" onClick={() => exec(UNO.Symbol)} />
            <MenuRow icon={SeparatorHorizontal} label="Page break" onClick={() => exec(UNO.PageBreak)} />
            <DropdownMenuSeparator />
            <MenuRow icon={Search} label="Find & replace" onClick={() => exec(UNO.Find)} />
            <MenuRow icon={SpellCheck} label="Spelling & grammar" onClick={() => exec(UNO.Spelling)} />
            <MenuRow icon={WholeWord} label="Word count" onClick={() => exec(UNO.WordCount)} />
            <MenuRow icon={GitCompare} label="Track changes" onClick={() => exec(UNO.TrackChanges)} />
            <MenuRow icon={Pilcrow} label="Formatting marks" onClick={() => exec(UNO.FormatMarks)} />
          </DropdownMenuContent>
        </DropdownMenu>

        {/* side menu: toggle the Format properties panel */}
        <Sep />
        <button
          type="button"
          title="Format panel"
          aria-pressed={panelOpen}
          onClick={onTogglePanel}
          className={cn(
            "flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-md transition-colors",
            panelOpen ? "bg-primary/15 text-primary" : "text-foreground hover:bg-foreground/10",
          )}
        >
          <PanelRight className="h-[17px] w-[17px]" />
        </button>
      </div>
    </div>
  );
}

// Paragraph-style gallery — a Trilli-themed grid of styles, each rendered in an
// approximation of its own look (like Word/Collabora's Styles gallery).
function StyleMenu({ exec }: { exec: Exec }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          title="Paragraph style"
          className={cn(selCls, "flex w-[112px] items-center justify-between gap-1")}
        >
          <span className="truncate">Styles</span>
          <ChevronDown className="h-3.5 w-3.5 flex-shrink-0 opacity-60" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="z-[120] w-[380px] p-2">
        <div className="grid grid-cols-2 gap-1.5">
          {STYLE_GALLERY.map((s) => (
            <DropdownMenuItem
              key={s.value}
              onSelect={() => exec(STYLE_CMD, styleArgs(s.value))}
              className="flex h-11 cursor-pointer flex-col justify-center rounded-md border border-border bg-card px-3 hover:border-primary/50 focus:border-primary/60 data-[highlighted]:border-primary/60 data-[highlighted]:bg-primary/5"
            >
              <span className={cn("truncate", s.preview)}>{s.label}</span>
            </DropdownMenuItem>
          ))}
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function MenuRow({ icon: Icon, label, onClick }: { icon?: LucideIcon; label: string; onClick: () => void }) {
  return (
    <DropdownMenuItem
      onSelect={() => onClick()}
      className="gap-2 text-[13px]"
    >
      {Icon ? <Icon className="h-4 w-4 text-muted-foreground" /> : <span className="w-4" />}
      {label}
    </DropdownMenuItem>
  );
}
