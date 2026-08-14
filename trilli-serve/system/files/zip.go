package files

import (
	"archive/zip"
	"context"
	"io"
	"path"

	"trilli/system/logging"
)

// FolderZipName returns the active folder's name for the archive filename
// (tenant-scoped; ErrNotFound when missing/trashed).
func (s *Service) FolderZipName(ctx context.Context, tenantID, folderID int64) (string, error) {
	var name string
	err := s.client.QueryRowContext(ctx, `
		SELECT name FROM folders
		 WHERE id = $1 AND tenant_id = $2 AND status = 'active'`, folderID, tenantID,
	).Scan(&name)
	if err != nil {
		return "", ErrFileNotFound
	}
	return name, nil
}

// WriteFolderZip streams the folder's entire subtree — files decrypted from
// the blob store — as a zip archive. Traversal is breadth-first over active
// folders/files only; every entry keeps its relative path so the receiver
// unpacks the exact tree. Returns the number of archive bytes written.
func (s *Service) WriteFolderZip(ctx context.Context, tenantID, rootID int64, w io.Writer) (int64, error) {
	cw := &countingWriter{w: w}
	zw := zip.NewWriter(cw)

	type node struct {
		id  int64
		rel string
	}
	queue := []node{{rootID, ""}}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]

		// subfolders
		rows, err := s.client.QueryContext(ctx, `
			SELECT id, name FROM folders
			 WHERE parent_folder_id = $1 AND tenant_id = $2 AND status = 'active'
			 ORDER BY name`, n.id, tenantID)
		if err != nil {
			zw.Close()
			return cw.n, err
		}
		type sub struct {
			id   int64
			name string
		}
		var subs []sub
		for rows.Next() {
			var f sub
			if rows.Scan(&f.id, &f.name) == nil {
				subs = append(subs, f)
			}
		}
		rows.Close()
		for _, f := range subs {
			rel := path.Join(n.rel, f.name)
			// explicit directory entry so empty folders survive the round trip
			if _, err := zw.Create(rel + "/"); err != nil {
				zw.Close()
				return cw.n, err
			}
			queue = append(queue, node{f.id, rel})
		}

		// files in this folder
		frows, err := s.client.QueryContext(ctx, `
			SELECT id, name FROM files
			 WHERE parent_folder_id = $1 AND tenant_id = $2 AND status = 'active'
			 ORDER BY name`, n.id, tenantID)
		if err != nil {
			zw.Close()
			return cw.n, err
		}
		type fitem struct {
			id   int64
			name string
		}
		var items []fitem
		for frows.Next() {
			var f fitem
			if frows.Scan(&f.id, &f.name) == nil {
				items = append(items, f)
			}
		}
		frows.Close()
		for _, it := range items {
			_, rc, err := s.Download(ctx, tenantID, it.id)
			if err != nil {
				logging.Error(packageName, "zip: open file %d: %v", it.id, err)
				continue // best-effort: skip unreadable entries, keep the archive
			}
			entry, err := zw.Create(path.Join(n.rel, it.name))
			if err != nil {
				rc.Close()
				zw.Close()
				return cw.n, err
			}
			if _, err := io.Copy(entry, rc); err != nil {
				rc.Close()
				zw.Close()
				return cw.n, err
			}
			rc.Close()
		}
	}
	return cw.n, zw.Close()
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
