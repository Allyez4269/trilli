package auth

import "testing"

// TestCanWriteAt covers the workspace-root write matrix: whole-workspace
// members may write at their assigned workspace's root; folder-scoped members
// and viewers may not; owners/admins are unrestricted.
func TestCanWriteAt(t *testing.T) {
	ws42 := int64(42)
	ws99 := int64(99)
	folder7 := int64(7)

	ownerAccess := &FolderAccess{Full: true, Write: true}
	wholeWsMember := &FolderAccess{Write: true, Folders: map[int64]bool{7: true}, Workspaces: map[int64]bool{42: true}}
	scopedMember := &FolderAccess{Write: true, Folders: map[int64]bool{7: true}, Workspaces: map[int64]bool{}}
	viewer := &FolderAccess{Write: false, Folders: map[int64]bool{7: true}, Workspaces: map[int64]bool{42: true}}

	cases := []struct {
		name   string
		a      *FolderAccess
		folder *int64
		ws     *int64
		want   bool
	}{
		{"nil access unrestricted", nil, nil, nil, true},
		{"owner at root, no workspace", ownerAccess, nil, nil, true},
		{"owner at root of any workspace", ownerAccess, nil, &ws99, true},

		// the reported bug: whole-workspace member at their workspace's root
		{"whole-ws member at assigned workspace root", wholeWsMember, nil, &ws42, true},
		{"whole-ws member at OTHER workspace root", wholeWsMember, nil, &ws99, false},
		{"whole-ws member at root without workspace", wholeWsMember, nil, nil, false},
		{"whole-ws member inside granted folder", wholeWsMember, &folder7, &ws42, true},

		// folder-scoped members stay denied at root (unchanged behavior)
		{"scoped member at workspace root", scopedMember, nil, &ws42, false},
		{"scoped member inside granted folder", scopedMember, &folder7, nil, true},

		// viewers can never write, even with a whole-workspace assignment
		{"viewer at assigned workspace root", viewer, nil, &ws42, false},
		{"viewer inside granted folder", viewer, &folder7, &ws42, false},
	}
	for _, c := range cases {
		if got := c.a.CanWriteAt(c.folder, c.ws); got != c.want {
			t.Errorf("%s: CanWriteAt = %v, want %v", c.name, got, c.want)
		}
	}
}
