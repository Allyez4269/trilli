// UNO command layer for the Trilli SLIDES (Collabora Impress) editor. Impress
// text formatting inside text boxes uses the WRITER-style commands (Bold/Italic/
// Underline/Strikeout, LeftPara/CenterPara/RightPara/JustifyPara, DefaultBullet/
// DefaultNumbering, font name/size/color) — NOT Calc's Align*/NumberFormat — so
// Slides re-uses those shared helpers from uno.ts plus presentation-specific
// dispatches (new/duplicate/delete slide, layout, insert shape/textbox, present).
//
// EVERY command below was verified present in the running engine's bundle.js.
// NOTE: .uno:Presentation (present-from-start) is ABSENT in this build — use
// .uno:PresentationCurrentSlide for "Present". As elsewhere, a parameterized
// command needs the EXACT dotted-member Arg name or it's silently ignored.
import {
  COLOR_CMD,
  FONT_NAME_CMD,
  FONT_SIZE_CMD,
  FONT_SIZES,
  FONTS,
  HIGHLIGHT_CMD,
  colorArgs,
  fontNameArgs,
  fontSizeArgs,
  hexToRgbInt,
  highlightArgs,
  isActive,
} from "./uno";

// Re-export the shared bits so the Slides components import from one place.
export {
  COLOR_CMD,
  FONT_NAME_CMD,
  FONT_SIZE_CMD,
  FONT_SIZES,
  FONTS,
  HIGHLIGHT_CMD,
  colorArgs,
  fontNameArgs,
  fontSizeArgs,
  hexToRgbInt,
  highlightArgs,
  isActive,
};

export const SLIDES_UNO = {
  // history + tools
  Undo: ".uno:Undo",
  Redo: ".uno:Redo",
  Print: ".uno:Print",
  FormatPaint: ".uno:FormatPaintbrush",
  ClearFormat: ".uno:SetDefault", // Impress clear-direct-formatting (not ResetAttributes)
  // zoom (stepwise)
  ZoomIn: ".uno:ZoomPlus",
  ZoomOut: ".uno:ZoomMinus",
  // slides
  NewSlide: ".uno:InsertSlide",
  DuplicateSlide: ".uno:DuplicateSlide",
  DeleteSlide: ".uno:DeleteSlide",
  HideSlide: ".uno:HideSlide", // toggle
  Layout: ".uno:AssignLayout",
  LayoutDialog: ".uno:ModifyPage",
  // font size
  Grow: ".uno:Grow",
  Shrink: ".uno:Shrink",
  // character toggles (engine reports state via CommandStateChanged)
  Bold: ".uno:Bold",
  Italic: ".uno:Italic",
  Underline: ".uno:Underline",
  Strike: ".uno:Strikeout",
  // alignment — Writer-style (Impress text boxes), NOT Calc Align*
  AlignLeft: ".uno:LeftPara",
  AlignCenter: ".uno:CenterPara",
  AlignRight: ".uno:RightPara",
  AlignJustify: ".uno:JustifyPara",
  // lists + indent
  Bullet: ".uno:DefaultBullet",
  Number: ".uno:DefaultNumbering",
  IndentInc: ".uno:IncrementIndent",
  IndentDec: ".uno:DecrementIndent",
  // line spacing
  Spacing1: ".uno:SpacePara1",
  Spacing15: ".uno:SpacePara15",
  Spacing2: ".uno:SpacePara2",
  // insert. NB: ".uno:Text" alone only ARMS a draw tool (user must drag) and is
  // a no-op over our postMessage path — the "?CreateDirectly:bool=true" arg makes
  // the engine drop in a default text box immediately (this is exactly what the
  // engine's own inserttextbox action sends). Likewise bare ".uno:BasicShapes"
  // does nothing; a shape needs a concrete name (".uno:BasicShapes.rectangle").
  TextBox: ".uno:Text?CreateDirectly:bool=true",
  Shape: ".uno:BasicShapes.rectangle",
  Line: ".uno:Line",
  Image: ".uno:InsertGraphic",
  Table: ".uno:InsertTable",
  Link: ".uno:HyperlinkDialog",
  Symbol: ".uno:InsertSymbol",
  // arrange (menu)
  BringFront: ".uno:BringToFront",
  SendBack: ".uno:SendToBack",
  ForwardOne: ".uno:ObjectForwardOne",
  BackOne: ".uno:ObjectBackOne",
  // present — from the CURRENT slide (.uno:Presentation / from-start is absent)
  Present: ".uno:PresentationCurrentSlide",
  // clipboard + tools
  Cut: ".uno:Cut",
  Copy: ".uno:Copy",
  Paste: ".uno:Paste",
  SelectAll: ".uno:SelectAll",
  Find: ".uno:SearchDialog",
  Spelling: ".uno:SpellDialog",
} as const;

// Slide auto-layout — .uno:AssignLayout with a WhatLayout id (LibreOffice
// AUTOLAYOUT enum). QA-verify each id actually switches the layout; a wrong id
// is a silent no-op (and "More layouts…" opens .uno:ModifyPage as the fallback).
export function layoutArgs(n: number) {
  return { WhatLayout: { type: "long", value: String(n) } };
}
export const SLIDE_LAYOUTS: { label: string; value: number }[] = [
  { label: "Title slide", value: 0 },
  { label: "Title, content", value: 1 },
  { label: "Title, two content", value: 3 },
  { label: "Title only", value: 19 },
  { label: "Blank", value: 20 },
];

export const LINE_SPACINGS: { label: string; cmd: string }[] = [
  { label: "Single", cmd: SLIDES_UNO.Spacing1 },
  { label: "1.5", cmd: SLIDES_UNO.Spacing15 },
  { label: "Double", cmd: SLIDES_UNO.Spacing2 },
];

// Insert-shape picker. Each name is verified in the engine bundle; the dispatch
// is ".uno:BasicShapes.<name>" (a concrete shape — bare ".uno:BasicShapes" is a
// no-op). Online inserts a default-sized shape immediately (no drag needed).
export function shapeCmd(name: string): string {
  return `.uno:BasicShapes.${name}`;
}
export const BASIC_SHAPES: { label: string; name: string }[] = [
  { label: "Rectangle", name: "rectangle" },
  { label: "Rounded rectangle", name: "round-rectangle" },
  { label: "Ellipse", name: "ellipse" },
  { label: "Circle", name: "circle" },
  { label: "Triangle", name: "isosceles-triangle" },
  { label: "Right triangle", name: "right-triangle" },
  { label: "Diamond", name: "diamond" },
  { label: "Pentagon", name: "pentagon" },
  { label: "Hexagon", name: "hexagon" },
  { label: "Cross", name: "cross" },
];
