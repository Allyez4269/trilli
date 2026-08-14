// Lightweight cross-component signalling over window CustomEvents — used so
// the sidebar, the file browser, and the trash page can stay in sync without
// a global store. Two concerns live here plus the shared internal-drag MIME.

// MIME carried by internal drag-and-drop of files/folders. The payload is a
// JSON string: { fileIds: number[]; folderIds: number[] }. Shared so any drop
// target (a folder row, a breadcrumb, the sidebar Trash bin) reads the same
// key the file browser writes on dragstart.
export const TRILLI_DRAG_MIME = "application/x-trilli-items";

// "trash-changed" — the trash contents changed (item added, restored, or
// purged). The sidebar listens to keep its Trash count badge live, rather than
// only refetching on navigation.
const TRASH_CHANGED = "trilli:trash-changed";

export function emitTrashChanged(): void {
  window.dispatchEvent(new Event(TRASH_CHANGED));
}

export function onTrashChanged(handler: () => void): () => void {
  window.addEventListener(TRASH_CHANGED, handler);
  return () => window.removeEventListener(TRASH_CHANGED, handler);
}

// "storage-changed" — the account's storage usage changed (file uploaded,
// deleted, or purged). The sidebar storage widget listens to refetch the quota
// immediately, rather than only refreshing on the next navigation.
const STORAGE_CHANGED = "trilli:storage-changed";

export function emitStorageChanged(): void {
  window.dispatchEvent(new Event(STORAGE_CHANGED));
}

export function onStorageChanged(handler: () => void): () => void {
  window.addEventListener(STORAGE_CHANGED, handler);
  return () => window.removeEventListener(STORAGE_CHANGED, handler);
}

// "items-trashed" — items were sent to trash from OUTSIDE the file browser
// (e.g. dragged onto the sidebar Trash bin), so an open Files view should
// reload to drop the now-trashed rows. The browser's own delete actions
// already reload locally, so they don't emit this one.
const ITEMS_TRASHED = "trilli:items-trashed";

export function emitItemsTrashed(): void {
  window.dispatchEvent(new Event(ITEMS_TRASHED));
}

export function onItemsTrashed(handler: () => void): () => void {
  window.addEventListener(ITEMS_TRASHED, handler);
  return () => window.removeEventListener(ITEMS_TRASHED, handler);
}
