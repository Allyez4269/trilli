// UNO command layer for the Trilli SHEETS (Collabora Calc) editor. Calc's
// dispatch commands genuinely differ from Writer's, so Sheets gets its own map
// instead of sharing uno.ts:
//   • text color is .uno:Color (NOT .uno:FontColor),
//   • cell fill is .uno:BackgroundColor (NOT .uno:CharBackColor),
//   • alignment is the Align* family (NOT LeftPara/CenterPara),
//   • cell styles use the "CellStyles" family (NOT "ParagraphStyles").
// Shared helpers (arg/state builders) are re-imported from uno.ts so there's a
// single source of truth.
//
// EVERY command below was verified to exist in the running engine's bundle.js.
// As in uno.ts, a parameterized command needs the EXACT dotted-member Arg name
// the engine itself emits (e.g. {"BackgroundColor.Color":{...}}) — the wrong
// name (or a Writer command on a Calc doc) is SILENTLY ignored.
import {
  COLOR_CMD,
  FONT_SIZE_CMD,
  FONT_SIZES,
  colorArgs,
  fontSizeArgs,
  hexToRgbInt,
  isActive,
} from "./uno";

// Re-export the shared bits so the Sheets components import from one place.
export { COLOR_CMD, FONT_SIZE_CMD, FONT_SIZES, colorArgs, fontSizeArgs, hexToRgbInt, isActive };

export const SHEETS_UNO = {
  // history
  Undo: ".uno:Undo",
  Redo: ".uno:Redo",
  // tools
  FormatPaint: ".uno:FormatPaintbrush",
  // zoom (stepwise — exact % isn't settable over postMessage)
  ZoomIn: ".uno:ZoomPlus",
  ZoomOut: ".uno:ZoomMinus",
  // number formats (toggles where noted)
  NumCurrency: ".uno:NumberFormatCurrency",
  NumPercent: ".uno:NumberFormatPercent",
  NumStandard: ".uno:NumberFormatStandard",
  NumDecimal: ".uno:NumberFormatDecimal",
  NumThousands: ".uno:NumberFormatThousands",
  NumDate: ".uno:NumberFormatDate",
  NumTime: ".uno:NumberFormatTime",
  NumScientific: ".uno:NumberFormatScientific",
  DecDecimals: ".uno:NumberFormatDecDecimals",
  IncDecimals: ".uno:NumberFormatIncDecimals",
  // font size
  Grow: ".uno:Grow",
  Shrink: ".uno:Shrink",
  // character toggles (engine reports state via CommandStateChanged)
  Bold: ".uno:Bold",
  Italic: ".uno:Italic",
  Underline: ".uno:Underline",
  Strike: ".uno:Strikeout",
  // alignment — CALC names (Writer's LeftPara/CenterPara no-op on a sheet)
  AlignLeft: ".uno:AlignLeft",
  AlignCenter: ".uno:AlignHorizontalCenter",
  AlignRight: ".uno:AlignRight",
  AlignJustify: ".uno:AlignBlock",
  AlignTop: ".uno:AlignTop",
  AlignMiddle: ".uno:AlignVCenter",
  AlignBottom: ".uno:AlignBottom",
  WrapText: ".uno:WrapText",
  // cells
  MergeCells: ".uno:ToggleMergeCells", // toggle; reports state
  MergeExplicit: ".uno:MergeCells",
  SplitCells: ".uno:SplitCell",
  // insert
  Link: ".uno:HyperlinkDialog",
  Chart: ".uno:InsertObjectChart",
  // data
  AutoFilter: ".uno:DataFilterAutoFilter", // toggle; reports state
  FreezePanes: ".uno:FreezePanes", // toggle; reports state
  FreezeRow: ".uno:FreezePanesRow",
  FreezeColumn: ".uno:FreezePanesColumn",
  // functions
  AutoSum: ".uno:AutoSum",
  FunctionDialog: ".uno:FunctionDialog",
  // clipboard
  Cut: ".uno:Cut",
  Copy: ".uno:Copy",
  Paste: ".uno:Paste",
  SelectAll: ".uno:SelectAll",
  Find: ".uno:SearchDialog",
  // dialogs (engine-owned, used as the v1 fallback for borders / text rotation /
  // the "Text" number format — same posture as Docs shipping Link/Table dialogs)
  FormatCells: ".uno:FormatCellDialog",
  Print: ".uno:Print",
} as const;

// Cell fill (background) — Calc's own command + dotted member, distinct from
// Writer's CharBackColor highlight.
export const FILL_COLOR_CMD = ".uno:BackgroundColor";
export function fillColorArgs(rgb: number) {
  return { "BackgroundColor.Color": { type: "long", value: String(rgb) } };
}

// Cell styles — .uno:StyleApply with the CellStyles family (NOT ParagraphStyles,
// which silently fails on a sheet).
export const CELL_STYLE_CMD = ".uno:StyleApply";
export function cellStyleArgs(style: string) {
  return {
    Style: { type: "string", value: style },
    FamilyName: { type: "string", value: "CellStyles" },
  };
}

// The built-in Calc cell styles surfaced in the "Default…" dropdown.
export const CELL_STYLES: { label: string; value: string }[] = [
  { label: "Default", value: "Default" },
  { label: "Good", value: "Good" },
  { label: "Neutral", value: "Neutral" },
  { label: "Bad", value: "Bad" },
  { label: "Heading 1", value: "Heading 1" },
  { label: "Heading 2", value: "Heading 2" },
];

// The "123" number-format dropdown — label + command. "Text" has no one-shot
// dispatch in this build, so it routes to the Format Cells dialog (Numbers tab).
export const NUMBER_FORMATS: { label: string; cmd: string }[] = [
  { label: "Automatic", cmd: SHEETS_UNO.NumStandard },
  { label: "Number", cmd: SHEETS_UNO.NumDecimal },
  { label: "Number — thousands", cmd: SHEETS_UNO.NumThousands },
  { label: "Percent", cmd: SHEETS_UNO.NumPercent },
  { label: "Currency", cmd: SHEETS_UNO.NumCurrency },
  { label: "Date", cmd: SHEETS_UNO.NumDate },
  { label: "Time", cmd: SHEETS_UNO.NumTime },
  { label: "Scientific", cmd: SHEETS_UNO.NumScientific },
  { label: "Text…", cmd: SHEETS_UNO.FormatCells },
];
