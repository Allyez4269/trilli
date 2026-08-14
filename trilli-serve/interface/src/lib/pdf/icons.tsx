// Trilli PDF — tool icons.
// PdfToolIcon — the generated Fluent-style image assets (same pipeline and
// visual language as the marketing-site suite icons: layered gradient badge
// over a white document plane, rendered to transparent PNGs). Master artwork
// lives in the generator script (scratchpad pdficons/gen.py pattern); to
// change an icon, edit the generator and re-render — never the PNGs.
export function PdfToolIcon({
  toolKey,
  size = 64,
  className,
}: {
  toolKey: string;
  size?: number;
  className?: string;
}) {
  return (
    <img
      src={`/img/pdf-tools/${toolKey}.png`}
      alt=""
      width={size}
      height={size}
      draggable={false}
      className={className}
      style={{ objectFit: "contain" }}
    />
  );
}
