// TrilliSheets toolbar — the Calc counterpart of DocsToolbar: a single compact
// rounded bar of Trilli-branded controls driving the headless Calc engine via
// UNO commands, with live active-state from CommandStateChanged. Mirrors
// DocsToolbar's structure/helpers exactly; only the controls (number formats,
// cell styles, Calc alignment, borders, merge, freeze, AutoSum) differ. Borders,
// text rotation and the "Text" number format ship v1 via the engine's Format
// Cells dialog (same posture as Docs' Link/Table dialogs).
import {
  AlignCenter,
  AlignJustify,
  AlignLeft,
  AlignRight,
  AlignVerticalJustifyCenter,
  ArrowDownToLine,
  ArrowUpToLine,
  Baseline,
  BarChart3,
  Bold,
  ChevronDown,
  Combine,
  DollarSign,
  Filter,
  Grid3x3,
  Image as ImageIcon,
  Italic,
  Link as LinkIcon,
  Minus,
  PaintBucket,
  Paintbrush,
  Percent,
  Plus,
  Printer,
  Redo2,
  RotateCw,
  Sigma,
  Snowflake,
  Strikethrough,
  Undo2,
  WrapText,
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
  CELL_STYLE_CMD,
  CELL_STYLES,
  COLOR_CMD,
  FILL_COLOR_CMD,
  FONT_SIZE_CMD,
  FONT_SIZES,
  NUMBER_FORMATS,
  SHEETS_UNO,
  cellStyleArgs,
  colorArgs,
  fillColorArgs,
  fontSizeArgs,
  hexToRgbInt,
  isActive,
} from "@/lib/productivity/sheets-uno";
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
      onMouseDown={(e) => e.preventDefault() /* keep the engine's cell selection */}
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

// Small-text button (the decimal-places controls have no clean glyph).
function TextBtn({ label, title, onClick }: { label: string; title: string; onClick: () => void }) {
  return (
    <button
      type="button"
      title={title}
      onMouseDown={(e) => e.preventDefault()}
      onClick={onClick}
      className="flex h-7 min-w-[28px] flex-shrink-0 items-center justify-center rounded-md px-1 text-[11px] font-medium leading-none text-foreground transition-colors hover:bg-foreground/10"
    >
      {label}
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

// Icon trigger + chevron that opens a dropdown (number formats, cell style,
// alignment, merge, freeze, borders, rotation). active highlights the trigger.
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

export function SheetsToolbar({
  exec,
  states,
  onInsertImage,
  onZoom,
  onPrint,
}: {
  exec: Exec;
  states: Record<string, string>;
  onInsertImage: () => void;
  onZoom: (dir: 1 | -1) => void;
  onPrint: () => void;
}) {
  return (
    // Self-contained formatting row (its own h-12 bar), like DocsToolbar — it
    // sits below the horizontal SheetsMenuBar in the editor's 3-row layout.
    <div className="flex h-12 flex-shrink-0 items-center border-b border-border bg-card px-3">
      <div className="flex h-9 w-full items-center gap-0.5 overflow-x-auto rounded-full bg-muted px-2.5 [-ms-overflow-style:none] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
        {/* history + print + format painter */}
        <ToolBtn icon={Undo2} title="Undo" onClick={() => exec(SHEETS_UNO.Undo)} />
        <ToolBtn icon={Redo2} title="Redo" onClick={() => exec(SHEETS_UNO.Redo)} />
        <ToolBtn icon={Printer} title="Print" onClick={onPrint} />
        <ToolBtn
          icon={Paintbrush}
          title="Clone formatting"
          active={isActive(states, SHEETS_UNO.FormatPaint)}
          onClick={() => exec(SHEETS_UNO.FormatPaint)}
        />
        <Sep />

        {/* zoom (stepwise; exact % isn't reported over postMessage, so we show
            no readout rather than a misleading static one — like DocsToolbar) */}
        <ToolBtn icon={ZoomOut} title="Zoom out" onClick={() => onZoom(-1)} />
        <ToolBtn icon={ZoomIn} title="Zoom in" onClick={() => onZoom(1)} />
        <Sep />

        {/* number formats */}
        <ToolBtn
          icon={DollarSign}
          title="Currency"
          active={isActive(states, SHEETS_UNO.NumCurrency)}
          onClick={() => exec(SHEETS_UNO.NumCurrency)}
        />
        <ToolBtn
          icon={Percent}
          title="Percent"
          active={isActive(states, SHEETS_UNO.NumPercent)}
          onClick={() => exec(SHEETS_UNO.NumPercent)}
        />
        <TextBtn label=".0" title="Decrease decimal places" onClick={() => exec(SHEETS_UNO.DecDecimals)} />
        <TextBtn label=".00" title="Increase decimal places" onClick={() => exec(SHEETS_UNO.IncDecimals)} />
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              title="Number format"
              onMouseDown={(e) => e.preventDefault()}
              className="flex h-7 flex-shrink-0 items-center gap-0.5 rounded-md px-1.5 text-[12px] font-semibold text-foreground transition-colors hover:bg-foreground/10"
            >
              123
              <ChevronDown className="h-3 w-3 flex-shrink-0 opacity-60" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="z-[120] w-52">
            {NUMBER_FORMATS.map((nf) => (
              <MenuRow key={nf.label} label={nf.label} active={isActive(states, nf.cmd)} onClick={() => exec(nf.cmd)} />
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
        <Sep />

        {/* cell style */}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              title="Cell style"
              onMouseDown={(e) => e.preventDefault()}
              className={cn(selCls, "flex w-[104px] items-center justify-between gap-1")}
            >
              <span className="truncate">Default</span>
              <ChevronDown className="h-3.5 w-3.5 flex-shrink-0 opacity-60" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="z-[120] w-44">
            {CELL_STYLES.map((s) => (
              <MenuRow key={s.value} label={s.label} onClick={() => exec(CELL_STYLE_CMD, cellStyleArgs(s.value))} />
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
        <Sep />

        {/* font size: − [size] + */}
        <ToolBtn icon={Minus} title="Decrease font size" onClick={() => exec(SHEETS_UNO.Shrink)} />
        <select
          className={cn(selCls, "w-[56px] text-center")}
          title="Font size"
          defaultValue="10"
          onChange={(e) => exec(FONT_SIZE_CMD, fontSizeArgs(Number(e.target.value)))}
        >
          {FONT_SIZES.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        <ToolBtn icon={Plus} title="Increase font size" onClick={() => exec(SHEETS_UNO.Grow)} />
        <Sep />

        {/* character formatting + text color */}
        <ToolBtn icon={Bold} title="Bold" active={isActive(states, SHEETS_UNO.Bold)} onClick={() => exec(SHEETS_UNO.Bold)} />
        <ToolBtn icon={Italic} title="Italic" active={isActive(states, SHEETS_UNO.Italic)} onClick={() => exec(SHEETS_UNO.Italic)} />
        <ToolBtn
          icon={Strikethrough}
          title="Strikethrough"
          active={isActive(states, SHEETS_UNO.Strike)}
          onClick={() => exec(SHEETS_UNO.Strike)}
        />
        <ColorBtn icon={Baseline} title="Text color" onPick={(h) => exec(COLOR_CMD, colorArgs(hexToRgbInt(h)))} />
        <Sep />

        {/* fill color */}
        <ColorBtn icon={PaintBucket} title="Fill color" onPick={(h) => exec(FILL_COLOR_CMD, fillColorArgs(hexToRgbInt(h)))} />
        <Sep />

        {/* borders (engine dialog v1) + merge */}
        <IconMenu icon={Grid3x3} title="Borders" width="w-48">
          <MenuRow label="Borders…" onClick={() => exec(SHEETS_UNO.FormatCells)} />
          <div className="px-2 py-1 text-[11px] text-muted-foreground">Opens Format Cells → Borders</div>
        </IconMenu>
        <IconMenu icon={Combine} title="Merge cells" active={isActive(states, SHEETS_UNO.MergeCells)}>
          <MenuRow
            label="Merge cells"
            active={isActive(states, SHEETS_UNO.MergeCells)}
            onClick={() => exec(SHEETS_UNO.MergeCells)}
          />
          <MenuRow
            label="Merge & center"
            onClick={() => {
              exec(SHEETS_UNO.MergeExplicit);
              exec(SHEETS_UNO.AlignCenter);
            }}
          />
          <DropdownMenuSeparator />
          <MenuRow label="Unmerge cells" onClick={() => exec(SHEETS_UNO.SplitCells)} />
        </IconMenu>
        <Sep />

        {/* alignment + wrap + rotation */}
        <IconMenu
          icon={AlignLeft}
          title="Horizontal align"
          active={
            isActive(states, SHEETS_UNO.AlignLeft) ||
            isActive(states, SHEETS_UNO.AlignCenter) ||
            isActive(states, SHEETS_UNO.AlignRight)
          }
        >
          <MenuRow icon={AlignLeft} label="Left" active={isActive(states, SHEETS_UNO.AlignLeft)} onClick={() => exec(SHEETS_UNO.AlignLeft)} />
          <MenuRow icon={AlignCenter} label="Center" active={isActive(states, SHEETS_UNO.AlignCenter)} onClick={() => exec(SHEETS_UNO.AlignCenter)} />
          <MenuRow icon={AlignRight} label="Right" active={isActive(states, SHEETS_UNO.AlignRight)} onClick={() => exec(SHEETS_UNO.AlignRight)} />
          <MenuRow icon={AlignJustify} label="Justify" active={isActive(states, SHEETS_UNO.AlignJustify)} onClick={() => exec(SHEETS_UNO.AlignJustify)} />
        </IconMenu>
        <IconMenu
          icon={AlignVerticalJustifyCenter}
          title="Vertical align"
          active={
            isActive(states, SHEETS_UNO.AlignTop) ||
            isActive(states, SHEETS_UNO.AlignMiddle) ||
            isActive(states, SHEETS_UNO.AlignBottom)
          }
        >
          <MenuRow icon={ArrowUpToLine} label="Top" active={isActive(states, SHEETS_UNO.AlignTop)} onClick={() => exec(SHEETS_UNO.AlignTop)} />
          <MenuRow icon={AlignVerticalJustifyCenter} label="Middle" active={isActive(states, SHEETS_UNO.AlignMiddle)} onClick={() => exec(SHEETS_UNO.AlignMiddle)} />
          <MenuRow icon={ArrowDownToLine} label="Bottom" active={isActive(states, SHEETS_UNO.AlignBottom)} onClick={() => exec(SHEETS_UNO.AlignBottom)} />
        </IconMenu>
        <ToolBtn icon={WrapText} title="Wrap text" active={isActive(states, SHEETS_UNO.WrapText)} onClick={() => exec(SHEETS_UNO.WrapText)} />
        <ToolBtn icon={RotateCw} title="Text rotation" onClick={() => exec(SHEETS_UNO.FormatCells)} />
        <Sep />

        {/* insert + data */}
        <ToolBtn icon={LinkIcon} title="Insert link" onClick={() => exec(SHEETS_UNO.Link)} />
        <ToolBtn icon={ImageIcon} title="Insert image" onClick={onInsertImage} />
        <ToolBtn icon={BarChart3} title="Insert chart" onClick={() => exec(SHEETS_UNO.Chart)} />
        <ToolBtn
          icon={Filter}
          title="Create filter"
          active={isActive(states, SHEETS_UNO.AutoFilter)}
          onClick={() => exec(SHEETS_UNO.AutoFilter)}
        />
        <IconMenu icon={Snowflake} title="Freeze" active={isActive(states, SHEETS_UNO.FreezePanes)}>
          <MenuRow
            label="Freeze panes"
            active={isActive(states, SHEETS_UNO.FreezePanes)}
            onClick={() => exec(SHEETS_UNO.FreezePanes)}
          />
          <MenuRow label="Freeze first row" onClick={() => exec(SHEETS_UNO.FreezeRow)} />
          <MenuRow label="Freeze first column" onClick={() => exec(SHEETS_UNO.FreezeColumn)} />
        </IconMenu>
        <Sep />

        {/* functions */}
        <ToolBtn icon={Sigma} title="Sum (AutoSum)" onClick={() => exec(SHEETS_UNO.AutoSum)} />
      </div>
    </div>
  );
}
