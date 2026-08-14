# Trilli PDF tool icons — same Fluent-style layered language as the suite
# icons (suiteicons/gen2.py): white document plane + gradient badge plane with
# cast shadows, per-tool brand triad derived from the tool's card color.

def mix(a, b, t):
    a = a.lstrip("#"); b = b.lstrip("#")
    return "#" + "".join(f"{round(int(a[i:i+2],16)+(int(b[i:i+2],16)-int(a[i:i+2],16))*t):02X}" for i in (0,2,4))

TOOLS = {  # key -> card color (from lib/pdf/tools.ts)
    "merge": "#ee5b4e", "rotate": "#c4589f", "nup": "#6a5bd6", "info": "#5b9bd5",
    "split": "#ee5b4e", "delete-pages": "#d64545", "extract-pages": "#6a5bd6",
    "image-to-pdf": "#e8ae3c", "extract-images": "#17a398", "pdf-to-image": "#5b9bd5",
    "watermark": "#9e56a5", "page-numbers": "#17a398", "compress": "#5cbb6a",
    "protect": "#4c86d9", "unlock": "#4c86d9",
}

def triad(mid):
    return dict(dark=mix(mid, "#000000", 0.38), mid=mid, light=mix(mid, "#FFFFFF", 0.28))

def defs(app, b):
    return f"""
    <linearGradient id="badge-{app}" x1="0" y1="1" x2="1" y2="0">
      <stop offset="0" stop-color="{b['dark']}"/><stop offset="0.5" stop-color="{b['mid']}"/><stop offset="1" stop-color="{b['light']}"/>
    </linearGradient>
    <linearGradient id="sheet-{app}" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#FFFFFF"/><stop offset="1" stop-color="#E7EEF8"/>
    </linearGradient>
    <filter id="amb-{app}" x="-30%" y="-30%" width="160%" height="160%">
      <feDropShadow dx="0" dy="2.2" stdDeviation="3" flood-color="#0A2540" flood-opacity="0.20"/>
    </filter>
    <filter id="cast-{app}" x="-40%" y="-40%" width="180%" height="180%">
      <feDropShadow dx="2.5" dy="4" stdDeviation="4.5" flood-color="#0A2540" flood-opacity="0.38"/>
    </filter>"""

def lines(x, w, ys, color, h=4.5, short=None):
    out = ""
    for i, y in enumerate(ys):
        ww = short if (short and i == len(ys) - 1) else w
        out += f'<rect x="{x}" y="{y}" width="{ww}" height="{h}" rx="{h/2}" fill="{color}"/>'
    return out

def sheet(app, tint):
    return f"""
    <g filter="url(#amb-{app})">
      <rect x="26" y="8" width="62" height="80" rx="7" fill="url(#sheet-{app})"/>
      {lines(55, 26, [17, 28], tint)}{lines(55, 26, [44, 55, 66], tint, short=16)}
    </g>"""

def badge(app, glyph):
    return f"""
    <g filter="url(#cast-{app})">
      <rect x="6" y="26" width="44" height="44" rx="9" fill="url(#badge-{app})"/>
      <path d="M15 26 h26 a9 9 0 0 1 9 9 v4 h-44 v-4 a9 9 0 0 1 9-9 z" fill="#ffffff" opacity="0.14"/>
      {glyph}
    </g>"""

W = "#ffffff"
S = f'stroke="{W}" fill="none" stroke-linecap="round" stroke-linejoin="round"'
# glyphs live in the badge box (6..50 x, 26..70 y), center (28,48)
GLYPHS = {
  "merge":
    f'<path d="M17 36 L25 44 M17 60 L25 52" {S} stroke-width="3.4"/>'
    f'<path d="M14 36 h5 v5 M14 60 h5 v-5" {S} stroke-width="3.4"/>'
    f'<path d="M26 48 h9" {S} stroke-width="3.6"/>'
    f'<path d="M34 42.5 L40.5 48 L34 53.5 Z" fill="{W}"/>',
  "rotate":
    f'<path d="M38.5 43 a11.5 11.5 0 1 0 1.5 8" {S} stroke-width="3.8"/>'
    f'<path d="M39.5 34.5 v9 h-9 Z" fill="{W}"/>',
  "nup":
    f'<rect x="16" y="36" width="10.5" height="10.5" rx="2.4" fill="{W}"/>'
    f'<rect x="29.5" y="36" width="10.5" height="10.5" rx="2.4" fill="{W}" opacity="0.62"/>'
    f'<rect x="16" y="49.5" width="10.5" height="10.5" rx="2.4" fill="{W}" opacity="0.62"/>'
    f'<rect x="29.5" y="49.5" width="10.5" height="10.5" rx="2.4" fill="{W}"/>',
  "info":
    f'<circle cx="28" cy="48" r="13.5" {S} stroke-width="3.4"/>'
    f'<circle cx="28" cy="42" r="2.3" fill="{W}"/>'
    f'<path d="M28 47.5 v8.5" {S} stroke-width="3.6"/>',
  "split":
    f'<path d="M23 48 h-4" {S} stroke-width="3.6"/><path d="M20.5 42.5 L14 48 L20.5 53.5 Z" fill="{W}"/>'
    f'<path d="M33 48 h4" {S} stroke-width="3.6"/><path d="M35.5 42.5 L42 48 L35.5 53.5 Z" fill="{W}"/>'
    f'<path d="M28 34 v28" {S} stroke-width="3" stroke-dasharray="4.5 4"/>',
  "delete-pages":
    f'<rect x="17" y="34" width="22" height="28" rx="3.5" {S} stroke-width="3.2"/>'
    f'<path d="M22.5 42.5 L33.5 53.5 M33.5 42.5 L22.5 53.5" {S} stroke-width="3.6"/>',
  "extract-pages":
    f'<rect x="15" y="38" width="19" height="24" rx="3.2" {S} stroke-width="3.2"/>'
    f'<path d="M30 44 L41 33" {S} stroke-width="3.6"/>'
    f'<path d="M33 32 h9 v9" {S} stroke-width="3.4"/>',
  "image-to-pdf":
    f'<rect x="15" y="36" width="26" height="20" rx="3" {S} stroke-width="3.2"/>'
    f'<circle cx="22" cy="42.5" r="2.2" fill="{W}"/>'
    f'<path d="M18 52 l6-6 4 4 5-5 5 5" {S} stroke-width="2.8"/>'
    f'<path d="M28 58 v6 M24.5 60.5 L28 64.5 L31.5 60.5" {S} stroke-width="3.2"/>',
  "extract-images":
    f'<rect x="15" y="34" width="26" height="20" rx="3" {S} stroke-width="3.2"/>'
    f'<circle cx="22" cy="40.5" r="2.2" fill="{W}"/>'
    f'<path d="M18 50 l6-6 4 4 5-5 5 5" {S} stroke-width="2.8"/>'
    f'<path d="M28 58 v6 M24.5 61.5 L28 57.5 L31.5 61.5" {S} stroke-width="3.2" transform="translate(0 1)"/>',
  "pdf-to-image":
    f'<rect x="14" y="35" width="20" height="16" rx="2.6" {S} stroke-width="3"/>'
    f'<path d="M17 48 l4.5-4.5 3 3 4-4 3.5 3.5" {S} stroke-width="2.4"/>'
    f'<text x="28" y="65" text-anchor="middle" font-size="11.5" font-weight="800" fill="{W}" font-family="Inter, system-ui, sans-serif">PNG</text>',
  "watermark":
    f'<path d="M28 34 c-5 6.5-8 10.5-8 15 a8 8 0 0 0 16 0 c0-4.5-3-8.5-8-15 Z" {S} stroke-width="3.2"/>'
    f'<path d="M18 62 h20" {S} stroke-width="3.2" stroke-dasharray="4 3.5"/>',
  "page-numbers":
    f'<path d="M22 36 l-3.5 24 M34 36 l-3.5 24 M17.5 43.5 h21 M16 52.5 h21" {S} stroke-width="3.4"/>',
  "compress":
    f'<path d="M28 33 v9 M24 38.5 L28 42.5 L32 38.5" {S} stroke-width="3.4"/>'
    f'<path d="M28 63 v-9 M24 57.5 L28 53.5 L32 57.5" {S} stroke-width="3.4"/>'
    f'<path d="M17 48 h22" {S} stroke-width="3.6"/>',
  "protect":
    f'<path d="M28 33 l11 4 v8.5 c0 7.5-4.5 12.5-11 15.5 c-6.5-3-11-8-11-15.5 V37 Z" {S} stroke-width="3.2"/>'
    f'<path d="M23.5 48 l3.5 3.5 6-6.5" {S} stroke-width="3.2"/>',
  "unlock":
    f'<path d="M21 46 v-4.5 a7 7 0 0 1 13.6-2.3" {S} stroke-width="3.4"/>'
    f'<rect x="17.5" y="46" width="21" height="16" rx="3.5" fill="{W}"/>'
    f'<circle cx="28" cy="52.5" r="2.5" fill="url(#badge-unlock)"/>'
    f'<rect x="26.9" y="54" width="2.2" height="5" rx="1.1" fill="url(#badge-unlock)"/>',
}

def svg(key):
    b = triad(TOOLS[key])
    tint = mix(TOOLS[key], "#FFFFFF", 0.72)
    return f"""<svg id="i-{key}" width="512" height="512" viewBox="0 0 96 96" xmlns="http://www.w3.org/2000/svg">
  <defs>{defs(key, b)}</defs>
  {sheet(key, tint)}
  {badge(key, GLYPHS[key])}
</svg>"""

html = "<html><body style='margin:0;background:transparent;display:flex;flex-wrap:wrap'>"
for k in TOOLS:
    html += f"<div style='padding:10px'>{svg(k)}</div>"
html += "</body></html>"
import os
out = os.path.join(os.path.dirname(__file__), "icons.html")
open(out, "w").write(html)
print("wrote", out)

# Render (from trilli-site's venv, which has playwright+chromium):
#   python3 gen.py && python3 render.py
