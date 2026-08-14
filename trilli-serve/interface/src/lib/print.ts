// printDocument loads a printable document (a PDF, image, or text — all
// same-origin from our API) into a hidden iframe and opens the browser's print
// dialog. Same-origin means we can reach contentWindow to call print().
//
// Office docs and spreadsheets are printed via their server-converted PDF, which
// may take a moment to render on first use (LibreOffice conversion, then cached);
// the iframe's load event fires once the PDF is ready.
//
// Some browsers' embedded PDF viewers refuse a programmatic print(); in that
// case we fall back to opening the document in a new tab so the user can print
// from there.
export function printDocument(url: string): void {
  const prev = document.getElementById("trilli-print-frame");
  if (prev) prev.remove();

  const iframe = document.createElement("iframe");
  iframe.id = "trilli-print-frame";
  iframe.setAttribute("aria-hidden", "true");
  Object.assign(iframe.style, {
    position: "fixed",
    right: "0",
    bottom: "0",
    width: "0",
    height: "0",
    border: "0",
    visibility: "hidden",
  } as CSSStyleDeclaration);

  let printed = false;
  const fallback = () => {
    if (printed) return;
    window.open(url, "_blank", "noopener");
  };

  iframe.onload = () => {
    try {
      const win = iframe.contentWindow;
      if (!win) {
        fallback();
        return;
      }
      win.focus();
      win.print();
      printed = true;
    } catch {
      fallback();
    }
  };
  iframe.onerror = fallback;

  iframe.src = url;
  document.body.appendChild(iframe);
}
