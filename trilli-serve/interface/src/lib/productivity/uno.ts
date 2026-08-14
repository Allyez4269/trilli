// UNO command layer for the headless editor. The native React toolbars call
// these; TrilliEditor.exec() posts them to the engine via Send_UNO_Command.
// Command names + arg shapes verified against LibreOffice/Collabora UNO.

export const UNO = {
  // history
  Undo: ".uno:Undo",
  Redo: ".uno:Redo",
  // zoom (stepwise — engine has no clean set-exact-% over postMessage)
  ZoomIn: ".uno:ZoomPlus",
  ZoomOut: ".uno:ZoomMinus",
  // character toggles (engine reports state via CommandStateChanged)
  Bold: ".uno:Bold",
  Italic: ".uno:Italic",
  Underline: ".uno:Underline",
  Strike: ".uno:Strikeout",
  // paragraph alignment
  Left: ".uno:LeftPara",
  Center: ".uno:CenterPara",
  Right: ".uno:RightPara",
  Justify: ".uno:JustifyPara",
  // lists
  Bullet: ".uno:DefaultBullet",
  Number: ".uno:DefaultNumbering",
  // indent
  IndentInc: ".uno:IncrementIndent",
  IndentDec: ".uno:DecrementIndent",
  // formatting tools
  FormatPaint: ".uno:FormatPaintbrush",
  ClearFormat: ".uno:ResetAttributes",
  Grow: ".uno:Grow",
  Shrink: ".uno:Shrink",
  Sub: ".uno:SubScript",
  Super: ".uno:SuperScript",
  // line spacing
  Spacing1: ".uno:SpacePara1",
  Spacing15: ".uno:SpacePara15",
  Spacing2: ".uno:SpacePara2",
  // insert (these open the engine's own dialog for now; P3-G6 nativizes them)
  Link: ".uno:HyperlinkDialog",
  Table: ".uno:InsertTable",
  Image: ".uno:InsertGraphic",
  Symbol: ".uno:InsertSymbol",
  Comment: ".uno:InsertAnnotation",
  PageBreak: ".uno:InsertPagebreak",
  // clipboard
  Cut: ".uno:Cut",
  Copy: ".uno:Copy",
  Paste: ".uno:Paste",
  SelectAll: ".uno:SelectAll",
  // change case
  CaseUpper: ".uno:ChangeCaseToUpper",
  CaseLower: ".uno:ChangeCaseToLower",
  CaseTitle: ".uno:ChangeCaseToTitleCase",
  // review
  Spelling: ".uno:SpellingAndGrammarDialog",
  TrackChanges: ".uno:TrackChanges",
  WordCount: ".uno:WordCountDialog",
  // tools
  Find: ".uno:SearchDialog",
  FormatMarks: ".uno:ControlCodes",
  Print: ".uno:Print",
} as const;

// Parameterized commands need an Args object in the UNO JSON shape
// ({ name: { type, value } }). Arg NAMES + (string) values match exactly what the
// engine itself sends, e.g. {"FontHeight.Height":{"type":"float","value":"12"}} —
// the dotted member name is REQUIRED or the command is silently ignored (this is
// why font/size weren't applying).
export function fontNameArgs(name: string) {
  return { "CharFontName.FamilyName": { type: "string", value: name } };
}
export function fontSizeArgs(pt: number) {
  return { "FontHeight.Height": { type: "float", value: String(pt) } };
}
// UNO Color is a 24-bit RGB integer (0xRRGGBB).
export function colorArgs(rgb: number) {
  return { Color: { type: "long", value: String(rgb) } };
}
export function styleArgs(style: string) {
  return {
    Style: { type: "string", value: style },
    FamilyName: { type: "string", value: "ParagraphStyles" },
  };
}
export function highlightArgs(rgb: number) {
  return { CharBackColor: { type: "long", value: String(rgb) } };
}
export const COLOR_CMD = ".uno:Color";
export const HIGHLIGHT_CMD = ".uno:CharBackColor";
export const FONT_NAME_CMD = ".uno:CharFontName";
export const FONT_SIZE_CMD = ".uno:FontHeight";
export const STYLE_CMD = ".uno:StyleApply";

export function hexToRgbInt(hex: string): number {
  return parseInt(hex.replace(/^#/, ""), 16) || 0;
}

export const FONT_SIZES = [8, 9, 10, 11, 12, 14, 16, 18, 20, 24, 28, 32, 36, 48, 72];
export const FONTS = [
  "Arial",
  "Calibri",
  "Carlito",
  "Cantarell",
  "Courier New",
  "DejaVu Sans",
  "DejaVu Serif",
  "Georgia",
  "Lato",
  "Liberation Sans",
  "Liberation Serif",
  "Montserrat",
  "Noto Sans",
  "Noto Serif",
  "Open Sans",
  "Roboto",
  "Times New Roman",
  "Verdana",
];
export const PARA_STYLES: { label: string; value: string }[] = [
  { label: "Normal", value: "Default Paragraph Style" },
  { label: "Title", value: "Title" },
  { label: "Subtitle", value: "Subtitle" },
  { label: "Heading 1", value: "Heading 1" },
  { label: "Heading 2", value: "Heading 2" },
  { label: "Heading 3", value: "Heading 3" },
];

// Visual styles gallery — each entry renders its own name in an approximation of
// the style (Trilli-themed), like the Word/Collabora gallery. `value` is the
// LibreOffice paragraph-style name passed to .uno:StyleApply.
export const STYLE_GALLERY: { label: string; value: string; preview: string }[] = [
  { label: "Normal", value: "Default Paragraph Style", preview: "text-[13px] text-foreground" },
  { label: "Title", value: "Title", preview: "text-[20px] font-semibold text-foreground" },
  { label: "Subtitle", value: "Subtitle", preview: "text-[13px] text-muted-foreground" },
  { label: "Heading 1", value: "Heading 1", preview: "text-[17px] font-bold text-foreground" },
  { label: "Heading 2", value: "Heading 2", preview: "text-[15px] font-bold text-foreground" },
  { label: "Heading 3", value: "Heading 3", preview: "text-[14px] font-semibold text-primary" },
  { label: "Heading 4", value: "Heading 4", preview: "text-[13px] font-semibold italic text-primary" },
  { label: "Heading 5", value: "Heading 5", preview: "text-[12px] font-semibold text-foreground" },
  { label: "Block Quotation", value: "Quotations", preview: "text-[13px] italic text-muted-foreground" },
  { label: "Preformatted", value: "Preformatted Text", preview: "font-mono text-[12px] text-foreground" },
  { label: "Caption", value: "Caption", preview: "text-[11px] text-muted-foreground" },
];

// True when CommandStateChanged reports this command as active ("true").
export function isActive(states: Record<string, string>, command: string): boolean {
  return states[command] === "true";
}
