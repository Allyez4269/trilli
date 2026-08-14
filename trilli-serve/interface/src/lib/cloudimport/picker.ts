// Loads Google's Picker JS API on demand and opens the Drive file picker. With
// the drive.file scope the user can browse and select any of their files in the
// Picker; the app only receives access to what they pick.

/* eslint-disable @typescript-eslint/no-explicit-any */
let pickerReady: Promise<void> | null = null;

function loadScript(src: string): Promise<void> {
  return new Promise((resolve, reject) => {
    if (document.querySelector(`script[src="${src}"]`)) {
      resolve();
      return;
    }
    const s = document.createElement("script");
    s.src = src;
    s.async = true;
    s.defer = true;
    s.onload = () => resolve();
    s.onerror = () => reject(new Error(`failed to load ${src}`));
    document.head.appendChild(s);
  });
}

function loadPickerApi(): Promise<void> {
  if (pickerReady) return pickerReady;
  pickerReady = loadScript("https://apis.google.com/js/api.js").then(
    () =>
      new Promise<void>((resolve) => {
        (window as any).gapi.load("picker", { callback: () => resolve() });
      }),
  );
  return pickerReady;
}

export type PickedFile = { id: string; name: string; mimeType: string; sizeBytes: number };

export async function openDrivePicker(opts: {
  accessToken: string;
  apiKey: string;
  appId: string;
}): Promise<PickedFile[]> {
  await loadPickerApi();
  const g = (window as any).google;
  return new Promise<PickedFile[]>((resolve) => {
    const view = new g.picker.DocsView(g.picker.ViewId.DOCS)
      .setIncludeFolders(true)
      .setSelectFolderEnabled(false);
    const builder = new g.picker.PickerBuilder()
      .setOAuthToken(opts.accessToken)
      .setDeveloperKey(opts.apiKey)
      .enableFeature(g.picker.Feature.MULTISELECT_ENABLED)
      .addView(view)
      .setCallback((data: any) => {
        if (data.action === g.picker.Action.PICKED) {
          resolve(
            (data.docs || []).map((d: any) => ({
              id: d.id,
              name: d.name,
              mimeType: d.mimeType,
              sizeBytes: Number(d.sizeBytes || 0),
            })),
          );
        } else if (data.action === g.picker.Action.CANCEL) {
          resolve([]);
        }
      });
    if (opts.appId) builder.setAppId(opts.appId);
    builder.build().setVisible(true);
  });
}
