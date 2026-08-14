// TrilliSheets menu bar — the Calc counterpart of DocsMenuBar: the standard
// horizontal File / Edit / View / Insert / Format / Data / Tools / Help text
// menus above the toolbar. Structural clone of DocsMenuBar; only the items +
// commands differ (Calc UNO via SHEETS_UNO, app actions via the props). Its own
// h-8 row, rendered between EditorToolbar and SheetsToolbar (3-row layout).
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { SHEETS_UNO } from "@/lib/productivity/sheets-uno";

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

export function SheetsMenuBar({
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
        <Item label="Undo" onClick={() => exec(SHEETS_UNO.Undo)} />
        <Item label="Redo" onClick={() => exec(SHEETS_UNO.Redo)} />
        <DropdownMenuSeparator />
        <Item label="Cut" onClick={() => exec(SHEETS_UNO.Cut)} />
        <Item label="Copy" onClick={() => exec(SHEETS_UNO.Copy)} />
        <Item label="Paste" onClick={() => exec(SHEETS_UNO.Paste)} />
        <Item label="Select all" onClick={() => exec(SHEETS_UNO.SelectAll)} />
        <DropdownMenuSeparator />
        <Item label="Find & replace" onClick={() => exec(SHEETS_UNO.Find)} />
      </Menu>

      <Menu label="View">
        <Item label="Freeze panes" onClick={() => exec(SHEETS_UNO.FreezePanes)} />
        <Item label="Freeze first row" onClick={() => exec(SHEETS_UNO.FreezeRow)} />
        <Item label="Freeze first column" onClick={() => exec(SHEETS_UNO.FreezeColumn)} />
      </Menu>

      <Menu label="Insert">
        <Item label="Image" onClick={onInsertImage} />
        <Item label="Link" onClick={() => exec(SHEETS_UNO.Link)} />
        <Item label="Chart" onClick={() => exec(SHEETS_UNO.Chart)} />
        <DropdownMenuSeparator />
        <Item label="Function (Sum)" onClick={() => exec(SHEETS_UNO.AutoSum)} />
        <Item label="More functions…" onClick={() => exec(SHEETS_UNO.FunctionDialog)} />
      </Menu>

      <Menu label="Format">
        <Item label="Bold" onClick={() => exec(SHEETS_UNO.Bold)} />
        <Item label="Italic" onClick={() => exec(SHEETS_UNO.Italic)} />
        <Item label="Underline" onClick={() => exec(SHEETS_UNO.Underline)} />
        <Item label="Strikethrough" onClick={() => exec(SHEETS_UNO.Strike)} />
        <DropdownMenuSeparator />
        <Item label="Align left" onClick={() => exec(SHEETS_UNO.AlignLeft)} />
        <Item label="Align center" onClick={() => exec(SHEETS_UNO.AlignCenter)} />
        <Item label="Align right" onClick={() => exec(SHEETS_UNO.AlignRight)} />
        <Item label="Justify" onClick={() => exec(SHEETS_UNO.AlignJustify)} />
        <DropdownMenuSeparator />
        <Item label="Currency" onClick={() => exec(SHEETS_UNO.NumCurrency)} />
        <Item label="Percent" onClick={() => exec(SHEETS_UNO.NumPercent)} />
        <DropdownMenuSeparator />
        <Item label="Merge cells" onClick={() => exec(SHEETS_UNO.MergeCells)} />
        <Item label="Unmerge cells" onClick={() => exec(SHEETS_UNO.SplitCells)} />
        <Item label="Wrap text" onClick={() => exec(SHEETS_UNO.WrapText)} />
        <DropdownMenuSeparator />
        <Item label="Format cells…" onClick={() => exec(SHEETS_UNO.FormatCells)} />
      </Menu>

      <Menu label="Data">
        <Item label="Create / remove filter" onClick={() => exec(SHEETS_UNO.AutoFilter)} />
      </Menu>

      <Menu label="Tools">
        <Item label="Sum (AutoSum)" onClick={() => exec(SHEETS_UNO.AutoSum)} />
        <Item label="Function wizard…" onClick={() => exec(SHEETS_UNO.FunctionDialog)} />
      </Menu>

      <Menu label="Help">
        <Item label="Trilli Sheets — preview" disabled />
      </Menu>
      <div className="ml-auto flex items-center pl-3">{presence}</div>
    </div>
  );
}
