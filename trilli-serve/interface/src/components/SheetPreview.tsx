import { useEffect, useMemo, useState } from "react";
import { Loader2 } from "lucide-react";

import { previewUrl } from "@/lib/api";
import { cn } from "@/lib/utils";

// SheetPreview renders a spreadsheet (xlsx/xls/ods…) as an interactive,
// read-only grid that mimics Excel/Sheets: A/B/C column letters, row numbers,
// gridlines, sticky headers, and sheet tabs. The workbook is parsed entirely
// in the browser from the original bytes — no server conversion, so it's
// instant and the file never leaves the session. SheetJS is dynamically
// imported so it only loads when a spreadsheet is actually previewed.

// Guard rails so a giant sheet can't lock up the tab. Beyond these we render a
// window and tell the user we've truncated.
const MAX_ROWS = 1000;
const MAX_COLS = 100;

// A parsed sheet ready to render: a dense grid of formatted cell strings plus
// the merge regions and per-cell numeric flag (for right-alignment).
interface ParsedSheet {
  name: string;
  rows: Cell[][]; // rows[r][c]
  nRows: number;
  nCols: number;
  truncated: boolean;
}

interface Cell {
  text: string;
  numeric: boolean;
  // Set on the top-left cell of a merge; covered cells are marked hidden.
  rowSpan?: number;
  colSpan?: number;
  hidden?: boolean;
}

interface Workbook {
  sheetNames: string[];
  sheets: Record<string, ParsedSheet>;
}

export default function SheetPreview({ fileId, name }: { fileId: number; name: string }) {
  const [wb, setWb] = useState<Workbook | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [active, setActive] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setWb(null);
    setError(null);
    setActive(0);
    (async () => {
      try {
        const [XLSX, res] = await Promise.all([
          import("xlsx"),
          fetch(previewUrl(fileId), { credentials: "same-origin" }),
        ]);
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const buf = await res.arrayBuffer();
        if (cancelled) return;
        const book = XLSX.read(buf, { type: "array", cellStyles: false });
        const parsed = parseWorkbook(XLSX, book);
        if (!cancelled) setWb(parsed);
      } catch {
        if (!cancelled) setError("We couldn't open this spreadsheet.");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [fileId]);

  if (error) {
    return (
      <div className="flex h-full w-full items-center justify-center rounded-lg bg-card text-sm text-muted-foreground shadow-2xl">
        {error}
      </div>
    );
  }
  if (!wb) {
    return (
      <div className="flex flex-col items-center gap-3 text-white/70">
        <Loader2 className="h-6 w-6 animate-spin" />
        <p className="text-sm">Opening spreadsheet…</p>
      </div>
    );
  }

  const sheet = wb.sheets[wb.sheetNames[active]];

  return (
    <div className="flex h-full w-full flex-col overflow-hidden rounded-lg bg-white shadow-2xl">
      <Grid sheet={sheet} title={name} />
      {wb.sheetNames.length > 1 && (
        <SheetTabs names={wb.sheetNames} active={active} onSelect={setActive} />
      )}
    </div>
  );
}

function Grid({ sheet, title }: { sheet: ParsedSheet; title: string }) {
  // Column letters A, B, …, AA, … for the header row.
  const colLetters = useMemo(
    () => Array.from({ length: sheet.nCols }, (_, c) => colName(c)),
    [sheet.nCols],
  );

  if (sheet.nCols === 0 || sheet.nRows === 0) {
    return (
      <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
        “{sheet.name}” is empty.
      </div>
    );
  }

  return (
    <div className="flex-1 overflow-auto">
      <table className="border-separate border-spacing-0 text-[12.5px] text-foreground">
        <thead>
          <tr>
            {/* Corner cell */}
            <th className="sticky left-0 top-0 z-30 h-7 w-12 min-w-12 border-b border-r border-border bg-muted" />
            {colLetters.map((letter) => (
              <th
                key={letter}
                className="sticky top-0 z-20 h-7 min-w-[5.5rem] border-b border-r border-border bg-muted px-2 text-center text-[11px] font-semibold text-muted-foreground"
              >
                {letter}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {sheet.rows.map((row, r) => (
            <tr key={r}>
              {/* Row number */}
              <td className="sticky left-0 z-10 w-12 min-w-12 border-b border-r border-border bg-muted px-2 text-center text-[11px] font-medium text-muted-foreground">
                {r + 1}
              </td>
              {row.map((cell, c) =>
                cell.hidden ? null : (
                  <td
                    key={c}
                    rowSpan={cell.rowSpan}
                    colSpan={cell.colSpan}
                    title={cell.text || undefined}
                    className={cn(
                      "max-w-[22rem] truncate border-b border-r border-border px-2 py-1 align-top",
                      cell.numeric ? "text-right tabular-nums" : "text-left",
                    )}
                  >
                    {cell.text}
                  </td>
                ),
              )}
            </tr>
          ))}
        </tbody>
      </table>
      {sheet.truncated && (
        <p className="border-t border-border bg-muted/40 px-3 py-1.5 text-[11px] text-muted-foreground">
          Showing the first {Math.min(sheet.nRows, MAX_ROWS).toLocaleString()} rows ×{" "}
          {Math.min(sheet.nCols, MAX_COLS)} columns of “{title}”. Download the file to see everything.
        </p>
      )}
    </div>
  );
}

function SheetTabs({
  names,
  active,
  onSelect,
}: {
  names: string[];
  active: number;
  onSelect: (i: number) => void;
}) {
  return (
    <div className="flex flex-shrink-0 items-stretch gap-0.5 overflow-x-auto border-t border-border bg-muted/50 px-1.5 py-1">
      {names.map((name, i) => (
        <button
          key={`${name}-${i}`}
          onClick={() => onSelect(i)}
          className={cn(
            "max-w-[14rem] truncate rounded-md px-3 py-1 text-[12px] font-medium transition-colors",
            i === active
              ? "bg-white text-foreground shadow-sm ring-1 ring-border"
              : "text-muted-foreground hover:bg-white/60 hover:text-foreground",
          )}
          title={name}
        >
          {name}
        </button>
      ))}
    </div>
  );
}

// ----- parsing --------------------------------------------------------------

// parseWorkbook converts a SheetJS workbook into our dense, render-ready shape.
// We read each cell's formatted text (cell.w — the value with the file's own
// number/date format applied) so the grid shows "12,400" / "$1,200" / dates
// exactly as the author saw them.
function parseWorkbook(XLSX: typeof import("xlsx"), book: import("xlsx").WorkBook): Workbook {
  const sheets: Record<string, ParsedSheet> = {};
  for (const sheetName of book.SheetNames) {
    sheets[sheetName] = parseSheet(XLSX, sheetName, book.Sheets[sheetName]);
  }
  return { sheetNames: book.SheetNames, sheets };
}

function parseSheet(
  XLSX: typeof import("xlsx"),
  name: string,
  ws: import("xlsx").WorkSheet,
): ParsedSheet {
  const ref = ws["!ref"];
  if (!ref) return { name, rows: [], nRows: 0, nCols: 0, truncated: false };

  const full = XLSX.utils.decode_range(ref);
  const totalRows = full.e.r - full.s.r + 1;
  const totalCols = full.e.c - full.s.c + 1;
  const nRows = Math.min(totalRows, MAX_ROWS);
  const nCols = Math.min(totalCols, MAX_COLS);
  const truncated = totalRows > MAX_ROWS || totalCols > MAX_COLS;

  const rows: Cell[][] = [];
  for (let r = 0; r < nRows; r++) {
    const row: Cell[] = [];
    for (let c = 0; c < nCols; c++) {
      const addr = XLSX.utils.encode_cell({ r: full.s.r + r, c: full.s.c + c });
      const cell = ws[addr];
      if (!cell) {
        row.push({ text: "", numeric: false });
        continue;
      }
      const text = cell.w ?? (cell.v != null ? String(cell.v) : "");
      row.push({ text, numeric: cell.t === "n" });
    }
    rows.push(row);
  }

  applyMerges(XLSX, ws, rows, full, nRows, nCols);
  return { name, rows, nRows: totalRows, nCols: totalCols, truncated };
}

// applyMerges turns SheetJS merge regions into rowSpan/colSpan on the top-left
// cell and marks the covered cells hidden so the table renders one merged box.
function applyMerges(
  XLSX: typeof import("xlsx"),
  ws: import("xlsx").WorkSheet,
  rows: Cell[][],
  full: import("xlsx").Range,
  nRows: number,
  nCols: number,
) {
  const merges = ws["!merges"];
  if (!merges) return;
  for (const m of merges) {
    const r0 = m.s.r - full.s.r;
    const c0 = m.s.c - full.s.c;
    if (r0 < 0 || c0 < 0 || r0 >= nRows || c0 >= nCols) continue;
    const rEnd = Math.min(m.e.r - full.s.r, nRows - 1);
    const cEnd = Math.min(m.e.c - full.s.c, nCols - 1);
    rows[r0][c0].rowSpan = rEnd - r0 + 1;
    rows[r0][c0].colSpan = cEnd - c0 + 1;
    for (let r = r0; r <= rEnd; r++) {
      for (let c = c0; c <= cEnd; c++) {
        if (r === r0 && c === c0) continue;
        rows[r][c].hidden = true;
      }
    }
  }
  void XLSX;
}

// colName maps a 0-based column index to a spreadsheet letter (0->A, 26->AA).
function colName(index: number): string {
  let n = index;
  let s = "";
  do {
    s = String.fromCharCode(65 + (n % 26)) + s;
    n = Math.floor(n / 26) - 1;
  } while (n >= 0);
  return s;
}
