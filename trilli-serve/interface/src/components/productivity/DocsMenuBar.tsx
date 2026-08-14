// TrilliDocs menu bar — the Google-Docs-style text menus (File / Edit / View /
// Insert / Format / Tools / Help) above the toolbar. Items map to UNO commands
// (via exec) or app actions (Save). Dialog-backed items use the engine's own
// dialogs for now (P3-G6 nativizes).
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { UNO } from "@/lib/productivity/uno";

type Exec = (command: string, args?: Record<string, unknown>) => void;

function Item({ label, onClick, disabled }: { label: string; onClick?: () => void; disabled?: boolean }) {
  return (
    <DropdownMenuItem
      disabled={disabled}
      onSelect={() => onClick?.()}
      className="text-[13px]"
    >
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

export function DocsMenuBar({
  presence,
  exec,
  onSave,
  onSaveAs,
  onDownload,
  onPrint,
  onNew,
  onOpen,
  onInsertImage,
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
        <Item label="Undo" onClick={() => exec(UNO.Undo)} />
        <Item label="Redo" onClick={() => exec(UNO.Redo)} />
        <DropdownMenuSeparator />
        <Item label="Cut" onClick={() => exec(UNO.Cut)} />
        <Item label="Copy" onClick={() => exec(UNO.Copy)} />
        <Item label="Paste" onClick={() => exec(UNO.Paste)} />
        <Item label="Select all" onClick={() => exec(UNO.SelectAll)} />
        <DropdownMenuSeparator />
        <Item label="Find & replace" onClick={() => exec(UNO.Find)} />
      </Menu>

      <Menu label="View">
        <Item label="Formatting marks" onClick={() => exec(UNO.FormatMarks)} />
      </Menu>

      <Menu label="Insert">
        <Item label="Image" onClick={onInsertImage} />
        <Item label="Link" onClick={() => exec(UNO.Link)} />
        <Item label="Table" onClick={() => exec(UNO.Table)} />
        <Item label="Comment" onClick={() => exec(UNO.Comment)} />
        <Item label="Special character" onClick={() => exec(UNO.Symbol)} />
        <Item label="Page break" onClick={() => exec(UNO.PageBreak)} />
      </Menu>

      <Menu label="Format">
        <Item label="Bold" onClick={() => exec(UNO.Bold)} />
        <Item label="Italic" onClick={() => exec(UNO.Italic)} />
        <Item label="Underline" onClick={() => exec(UNO.Underline)} />
        <Item label="Strikethrough" onClick={() => exec(UNO.Strike)} />
        <DropdownMenuSeparator />
        <Item label="Align left" onClick={() => exec(UNO.Left)} />
        <Item label="Align center" onClick={() => exec(UNO.Center)} />
        <Item label="Align right" onClick={() => exec(UNO.Right)} />
        <Item label="Justify" onClick={() => exec(UNO.Justify)} />
        <DropdownMenuSeparator />
        <Item label="Line spacing 1.0" onClick={() => exec(UNO.Spacing1)} />
        <Item label="Line spacing 1.5" onClick={() => exec(UNO.Spacing15)} />
        <Item label="Line spacing 2.0" onClick={() => exec(UNO.Spacing2)} />
        <DropdownMenuSeparator />
        <Item label="Clear formatting" onClick={() => exec(UNO.ClearFormat)} />
      </Menu>

      <Menu label="Tools">
        <Item label="Spelling & grammar" onClick={() => exec(UNO.Spelling)} />
        <Item label="Word count" onClick={() => exec(UNO.WordCount)} />
        <Item label="Track changes" onClick={() => exec(UNO.TrackChanges)} />
      </Menu>

      <Menu label="Help">
        <Item label="Trilli Productivity — preview" disabled />
      </Menu>
      <div className="ml-auto flex items-center pl-3">{presence}</div>
    </div>
  );
}
