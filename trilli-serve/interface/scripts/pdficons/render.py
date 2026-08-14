# Renders icons.html (from gen.py) to transparent 512px PNGs in
# interface/public/img/pdf-tools/. Run with a python that has playwright.
import os
from playwright.sync_api import sync_playwright
HERE = os.path.dirname(os.path.abspath(__file__))
DEST = os.path.normpath(os.path.join(HERE, "..", "..", "public", "img", "pdf-tools"))
KEYS = ["merge","rotate","nup","info","split","delete-pages","extract-pages","image-to-pdf",
        "extract-images","pdf-to-image","watermark","page-numbers","compress","protect","unlock"]
os.makedirs(DEST, exist_ok=True)
with sync_playwright() as p:
    b = p.chromium.launch()
    pg = b.new_page(viewport={"width": 3400, "height": 1400})
    pg.goto("file://" + os.path.join(HERE, "icons.html"))
    pg.wait_for_timeout(500)
    for k in KEYS:
        pg.locator(f"#i-{k}").screenshot(path=os.path.join(DEST, f"{k}.png"), omit_background=True)
    b.close()
print("rendered", len(KEYS), "->", DEST)
