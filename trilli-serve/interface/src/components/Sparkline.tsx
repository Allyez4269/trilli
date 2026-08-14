import { useId } from "react";
import { cn } from "@/lib/utils";

// Dependency-free area+line sparkline for the dashboard stat tiles. Renders the
// real per-day series passed in; returns null for <2 points (a single day has
// no trend to draw). `stroke` is any CSS color (we pass brand hexes).
export function Sparkline({
  data,
  stroke = "#635BFF",
  className,
  width = 120,
  height = 34,
}: {
  data: number[];
  stroke?: string;
  className?: string;
  width?: number;
  height?: number;
}) {
  const gid = useId();
  if (!data || data.length < 2) return null;

  const max = Math.max(...data);
  const min = Math.min(...data);
  const range = max - min || 1;
  const pad = 3;
  const stepX = width / (data.length - 1);
  const yOf = (v: number) => pad + (height - pad * 2) * (1 - (v - min) / range);

  const pts = data.map((v, i) => [i * stepX, yOf(v)] as const);
  const line = pts
    .map(([x, y], i) => `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`)
    .join(" ");
  const area = `${line} L${width},${height} L0,${height} Z`;

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      className={cn("h-9 w-full", className)}
      aria-hidden
    >
      <defs>
        <linearGradient id={gid} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={stroke} stopOpacity="0.2" />
          <stop offset="100%" stopColor={stroke} stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={area} fill={`url(#${gid})`} />
      <path
        d={line}
        fill="none"
        stroke={stroke}
        strokeWidth={1.5}
        strokeLinecap="round"
        strokeLinejoin="round"
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
}
