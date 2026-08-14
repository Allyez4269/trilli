// TrilliSlides menu bar — the Impress counterpart of DocsMenuBar/SheetsMenuBar:
// the standard horizontal File / Edit / View / Insert / Format / Slide / Tools /
// Help text menus above the toolbar. Structural clone; only the items + commands
// differ (Impress UNO via SLIDES_UNO, app actions via the props).
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { SLIDES_UNO, SLIDE_LAYOUTS, layoutArgs } from "@/lib/productivity/slides-uno";

type Exec = (command: string, args?: Record<string, unknown>) => void;

function Item({ label, onClick, disabled }: { label: string; onClick?: () => void; disabled?: boolean }) {
  return (
    <DropdownMenuItem disabled={disabled} onSelect={() => onClick?.()} className="text-[13px]">
      {label}
    </DropdownMenuItem>
  );
}

function Menu({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="h-7 rounded-md px-2 text-[13px] text-foreground outline-none transition-colors hover:bg-foreground/10 data-[state=open]:bg-foreground/10"
        >
          {label}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="z-[120] w-56">
        {children}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function SlidesMenuBar({
  presence,
  exec,
  onSave,
  onSaveAs,
  onDownload,
  onPrint,
  onNew,
  onOpen,
  onInsertImage,
  onPresent,
}: {
  presence?: React.ReactNode;
  exec: Exec;
  onSave: () => void;
  onSaveAs: () => void;
  onDownload: () => void;
  onPrint: () => void;
  onNew: () => void;
  onOpen: () => void;
  onInsertImage: () => void;
  onPresent: () => void;
}) {
  return (
    <div className="flex h-8 flex-shrink-0 items-center gap-0.5 border-b border-border bg-card px-3">
      {/* presence slot renders at the row's right edge */}
      <Menu label="File">
        <Item label="New" onClick={onNew} />
        <Item label="Open" onClick={onOpen} />
        <DropdownMenuSeparator />
        <Item label="Save" onClick={onSave} />
        <Item label="Save As…" onClick={onSaveAs} />
        <DropdownMenuSeparator />
        <Item label="Download" onClick={onDownload} />
        <Item label="Print" onClick={onPrint} />
      </Menu>

      <Menu label="Edit">
        <Item label="Undo" onClick={() => exec(SLIDES_UNO.Undo)} />
        <Item label="Redo" onClick={() => exec(SLIDES_UNO.Redo)} />
        <DropdownMenuSeparator />
        <Item label="Cut" onClick={() => exec(SLIDES_UNO.Cut)} />
        <Item label="Copy" onClick={() => exec(SLIDES_UNO.Copy)} />
        <Item label="Paste" onClick={() => exec(SLIDES_UNO.Paste)} />
        <Item label="Select all" onClick={() => exec(SLIDES_UNO.SelectAll)} />
        <DropdownMenuSeparator />
        <Item label="Find & replace" onClick={() => exec(SLIDES_UNO.Find)} />
      </Menu>

      <Menu label="View">
        <Item label="Start presentation" onClick={onPresent} />
        <DropdownMenuSeparator />
        <Item label="Zoom in" onClick={() => exec(SLIDES_UNO.ZoomIn)} />
        <Item label="Zoom out" onClick={() => exec(SLIDES_UNO.ZoomOut)} />
      </Menu>

      <Menu label="Insert">
        <Item label="Text box" onClick={() => exec(SLIDES_UNO.TextBox)} />
        <Item label="Image" onClick={onInsertImage} />
        <Item label="Shape" onClick={() => exec(SLIDES_UNO.Shape)} />
        <Item label="Line" onClick={() => exec(SLIDES_UNO.Line)} />
        <Item label="Table" onClick={() => exec(SLIDES_UNO.Table)} />
        <Item label="Link" onClick={() => exec(SLIDES_UNO.Link)} />
        <Item label="Special character" onClick={() => exec(SLIDES_UNO.Symbol)} />
      </Menu>

      <Menu label="Format">
        <Item label="Bold" onClick={() => exec(SLIDES_UNO.Bold)} />
        <Item label="Italic" onClick={() => exec(SLIDES_UNO.Italic)} />
        <Item label="Underline" onClick={() => exec(SLIDES_UNO.Underline)} />
        <Item label="Strikethrough" onClick={() => exec(SLIDES_UNO.Strike)} />
        <DropdownMenuSeparator />
        <Item label="Align left" onClick={() => exec(SLIDES_UNO.AlignLeft)} />
        <Item label="Align center" onClick={() => exec(SLIDES_UNO.AlignCenter)} />
        <Item label="Align right" onClick={() => exec(SLIDES_UNO.AlignRight)} />
        <Item label="Justify" onClick={() => exec(SLIDES_UNO.AlignJustify)} />
        <DropdownMenuSeparator />
        <Item label="Bullets" onClick={() => exec(SLIDES_UNO.Bullet)} />
        <Item label="Numbering" onClick={() => exec(SLIDES_UNO.Number)} />
        <DropdownMenuSeparator />
        <Item label="Line spacing 1.0" onClick={() => exec(SLIDES_UNO.Spacing1)} />
        <Item label="Line spacing 1.5" onClick={() => exec(SLIDES_UNO.Spacing15)} />
        <Item label="Line spacing 2.0" onClick={() => exec(SLIDES_UNO.Spacing2)} />
        <DropdownMenuSeparator />
        <Item label="Clear formatting" onClick={() => exec(SLIDES_UNO.ClearFormat)} />
      </Menu>

      <Menu label="Slide">
        <Item label="New slide" onClick={() => exec(SLIDES_UNO.NewSlide)} />
        <Item label="Duplicate slide" onClick={() => exec(SLIDES_UNO.DuplicateSlide)} />
        <Item label="Delete slide" onClick={() => exec(SLIDES_UNO.DeleteSlide)} />
        <Item label="Hide slide" onClick={() => exec(SLIDES_UNO.HideSlide)} />
        <DropdownMenuSeparator />
        {SLIDE_LAYOUTS.map((l) => (
          <Item key={l.value} label={`Layout: ${l.label}`} onClick={() => exec(SLIDES_UNO.Layout, layoutArgs(l.value))} />
        ))}
        <Item label="More layouts…" onClick={() => exec(SLIDES_UNO.LayoutDialog)} />
        <DropdownMenuSeparator />
        <Item label="Present" onClick={onPresent} />
      </Menu>

      <Menu label="Tools">
        <Item label="Spelling" onClick={() => exec(SLIDES_UNO.Spelling)} />
        <Item label="Find & replace" onClick={() => exec(SLIDES_UNO.Find)} />
      </Menu>

      <Menu label="Help">
        <Item label="Trilli Slides — preview" disabled />
      </Menu>
      <div className="ml-auto flex items-center pl-3">{presence}</div>
    </div>
  );
}
