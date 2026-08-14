// Trilli PDF — the tool registry. One entry per native pdftools endpoint; this
// single source of truth drives both the launcher cards and the per-tool shell
// (inputs, params, result handling). Adding a tool = adding an entry here + the
// matching /api/pdf/<key> handler.
import {
  Combine,
  FileImage,
  FileOutput,
  Hash,
  ImagePlus,
  Images,
  Info,
  LayoutGrid,
  Lock,
  Minimize2,
  RotateCw,
  Scissors,
  Stamp,
  Trash2,
  Unlock,
  type LucideIcon,
} from "lucide-react";

export type PdfParam =
  | { kind: "text"; name: string; label: string; placeholder?: string; required?: boolean }
  | { kind: "password"; name: string; label: string; placeholder?: string; required?: boolean }
  | { kind: "select"; name: string; label: string; default: string; options: { value: string; label: string }[] };

export type PdfTool = {
  key: string; // matches POST /api/pdf/<key>
  name: string;
  blurb: string;
  category: (typeof PDF_CATEGORIES)[number];
  Icon: LucideIcon;
  color: string; // hex; tints the card icon + accents
  input: "single" | "multi"; // multi = 2+ files (merge, image→pdf)
  accept?: string[]; // input file extensions for the picker/upload (default ["pdf"])
  params: PdfParam[];
  result: "pdf" | "info";
  output?: "pdf" | "zip"; // output file type (default "pdf"); zip for multi-file results
  cta: string; // run-button label
  defaultName: string; // default output filename
};

export const PDF_CATEGORIES = ["Organize", "Convert", "Edit", "Optimize", "Secure"] as const;

const ORGANIZE = "#6a5bd6"; // Trilli indigo
const EDIT = "#17a398"; // teal
const OPTIMIZE = "#5cbb6a"; // green
const SECURE = "#4c86d9"; // steel blue

export const PDF_TOOLS: PdfTool[] = [
  {
    key: "merge",
    name: "Merge",
    blurb: "Combine several PDFs into one, in the order you choose.",
    category: "Organize",
    Icon: Combine,
    color: "#ee5b4e",
    input: "multi",
    params: [],
    result: "pdf",
    cta: "Merge PDFs",
    defaultName: "merged.pdf",
  },
  {
    key: "rotate",
    name: "Rotate",
    blurb: "Turn every page 90°, 180°, or 270°.",
    category: "Organize",
    Icon: RotateCw,
    color: "#c4589f",
    input: "single",
    params: [
      {
        kind: "select",
        name: "rotation",
        label: "Rotation",
        default: "90",
        options: [
          { value: "90", label: "90° clockwise" },
          { value: "180", label: "180°" },
          { value: "270", label: "270° (counter-clockwise)" },
        ],
      },
    ],
    result: "pdf",
    cta: "Rotate",
    defaultName: "rotated.pdf",
  },
  {
    key: "nup",
    name: "N-up",
    blurb: "Place multiple pages onto each sheet — handouts, thumbnails.",
    category: "Organize",
    Icon: LayoutGrid,
    color: ORGANIZE,
    input: "single",
    params: [
      {
        kind: "select",
        name: "n",
        label: "Pages per sheet",
        default: "2",
        options: [
          { value: "2", label: "2 per sheet" },
          { value: "4", label: "4 per sheet" },
          { value: "6", label: "6 per sheet" },
          { value: "9", label: "9 per sheet" },
        ],
      },
    ],
    result: "pdf",
    cta: "Create N-up",
    defaultName: "n-up.pdf",
  },
  {
    key: "info",
    name: "PDF info",
    blurb: "Inspect pages, dimensions, security, forms, and metadata.",
    category: "Organize",
    Icon: Info,
    color: "#5b9bd5",
    input: "single",
    params: [],
    result: "info",
    cta: "Inspect",
    defaultName: "",
  },
  {
    key: "watermark",
    name: "Watermark",
    blurb: "Stamp diagonal text across every page.",
    category: "Edit",
    Icon: Stamp,
    color: "#9e56a5",
    input: "single",
    params: [{ kind: "text", name: "text", label: "Watermark text", placeholder: "CONFIDENTIAL", required: true }],
    result: "pdf",
    cta: "Add watermark",
    defaultName: "watermarked.pdf",
  },
  {
    key: "page-numbers",
    name: "Page numbers",
    blurb: "Add page numbers to the footer of every page.",
    category: "Edit",
    Icon: Hash,
    color: EDIT,
    input: "single",
    params: [],
    result: "pdf",
    cta: "Add page numbers",
    defaultName: "numbered.pdf",
  },
  {
    key: "compress",
    name: "Compress",
    blurb: "Shrink file size by stripping redundancy and maxing compression.",
    category: "Optimize",
    Icon: Minimize2,
    color: OPTIMIZE,
    input: "single",
    params: [],
    result: "pdf",
    cta: "Compress",
    defaultName: "compressed.pdf",
  },
  {
    key: "protect",
    name: "Protect",
    blurb: "Encrypt the PDF with a password (AES-256).",
    category: "Secure",
    Icon: Lock,
    color: SECURE,
    input: "single",
    params: [
      { kind: "password", name: "user_password", label: "Password", placeholder: "Required to open", required: true },
      { kind: "password", name: "owner_password", label: "Owner password (optional)", placeholder: "Controls permissions" },
    ],
    result: "pdf",
    cta: "Protect",
    defaultName: "protected.pdf",
  },
  {
    key: "unlock",
    name: "Unlock",
    blurb: "Remove a known password from a protected PDF.",
    category: "Secure",
    Icon: Unlock,
    color: SECURE,
    input: "single",
    params: [{ kind: "password", name: "password", label: "Current password", required: true }],
    result: "pdf",
    cta: "Unlock",
    defaultName: "unlocked.pdf",
  },
  {
    key: "split",
    name: "Split",
    blurb: "Split a PDF into separate files every N pages (returns a ZIP).",
    category: "Organize",
    Icon: Scissors,
    color: "#ee5b4e",
    input: "single",
    params: [
      {
        kind: "select",
        name: "span",
        label: "Pages per file",
        default: "1",
        options: [
          { value: "1", label: "1 page each" },
          { value: "2", label: "2 pages each" },
          { value: "5", label: "5 pages each" },
          { value: "10", label: "10 pages each" },
        ],
      },
    ],
    result: "pdf",
    output: "zip",
    cta: "Split",
    defaultName: "split.zip",
  },
  {
    key: "delete-pages",
    name: "Delete pages",
    blurb: "Remove specific pages from a PDF.",
    category: "Organize",
    Icon: Trash2,
    color: "#d64545",
    input: "single",
    params: [{ kind: "text", name: "pages", label: "Pages to delete", placeholder: "e.g. 1-3, 5, 8", required: true }],
    result: "pdf",
    cta: "Delete pages",
    defaultName: "pages-removed.pdf",
  },
  {
    key: "extract-pages",
    name: "Extract pages",
    blurb: "Keep only the pages you choose (also reorders).",
    category: "Organize",
    Icon: FileOutput,
    color: "#6a5bd6",
    input: "single",
    params: [{ kind: "text", name: "pages", label: "Pages to keep", placeholder: "e.g. 1, 3-4, 7", required: true }],
    result: "pdf",
    cta: "Extract pages",
    defaultName: "extracted-pages.pdf",
  },
  {
    key: "image-to-pdf",
    name: "Image to PDF",
    blurb: "Combine images (JPG, PNG…) into one PDF, one per page.",
    category: "Convert",
    Icon: ImagePlus,
    color: "#e8ae3c",
    input: "multi",
    accept: ["png", "jpg", "jpeg", "webp", "gif", "tif", "tiff"],
    params: [],
    result: "pdf",
    cta: "Create PDF",
    defaultName: "images.pdf",
  },
  {
    key: "extract-images",
    name: "Extract images",
    blurb: "Pull every embedded image out of a PDF (returns a ZIP).",
    category: "Convert",
    Icon: Images,
    color: "#17a398",
    input: "single",
    params: [],
    result: "pdf",
    output: "zip",
    cta: "Extract images",
    defaultName: "images.zip",
  },
  {
    key: "pdf-to-image",
    name: "PDF to Image",
    blurb: "Render each page to a PNG image (returns a ZIP).",
    category: "Convert",
    Icon: FileImage,
    color: "#5b9bd5",
    input: "single",
    params: [
      {
        kind: "select",
        name: "dpi",
        label: "Quality",
        default: "150",
        options: [
          { value: "100", label: "Standard (100 DPI)" },
          { value: "150", label: "High (150 DPI)" },
          { value: "200", label: "Very high (200 DPI)" },
        ],
      },
    ],
    result: "pdf",
    output: "zip",
    cta: "Convert to images",
    defaultName: "pages.zip",
  },
];

export function pdfToolByKey(key: string | undefined): PdfTool | undefined {
  return PDF_TOOLS.find((t) => t.key === key);
}
