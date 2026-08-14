package files

import "context"

// TreeStats are the RECURSIVE contents of a location: every descendant folder
// and every file cascading through the whole subtree — what the Files status
// bar reports, Windows-properties style.
type TreeStats struct {
	Folders int64 `json:"folders"`
	Files   int64 `json:"files"`
}

// TreeStatsFor counts the full subtree under a folder (folderID set) or a
// workspace root (folderID nil + workspaceID). Active items only.
func (s *Service) TreeStatsFor(ctx context.Context, tenantID int64, folderID, workspaceID *int64) (TreeStats, error) {
	var st TreeStats
	if folderID != nil {
		err := s.client.QueryRowContext(ctx, `
			WITH RECURSIVE sub AS (
				SELECT id FROM folders
				 WHERE tenant_id = $1 AND status = 'active' AND parent_folder_id = $2
				UNION ALL
				SELECT f.id FROM folders f JOIN sub ON f.parent_folder_id = sub.id
				 WHERE f.tenant_id = $1 AND f.status = 'active'
			)
			SELECT (SELECT count(*) FROM sub),
			       (SELECT count(*) FROM files
			         WHERE tenant_id = $1 AND status = 'active'
			           AND (parent_folder_id = $2 OR parent_folder_id IN (SELECT id FROM sub)))`,
			tenantID, *folderID,
		).Scan(&st.Folders, &st.Files)
		return st, err
	}
	if workspaceID == nil {
		return st, nil
	}
	err := s.client.QueryRowContext(ctx, `
		WITH RECURSIVE sub AS (
			SELECT id FROM folders
			 WHERE tenant_id = $1 AND status = 'active'
			   AND parent_folder_id IS NULL AND workspace_id = $2
			UNION ALL
			SELECT f.id FROM folders f JOIN sub ON f.parent_folder_id = sub.id
			 WHERE f.tenant_id = $1 AND f.status = 'active'
		)
		SELECT (SELECT count(*) FROM sub),
		       (SELECT count(*) FROM files
		         WHERE tenant_id = $1 AND status = 'active'
		           AND ((parent_folder_id IS NULL AND workspace_id = $2)
		                OR parent_folder_id IN (SELECT id FROM sub)))`,
		tenantID, *workspaceID,
	).Scan(&st.Folders, &st.Files)
	return st, err
}
