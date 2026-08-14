package officesessions

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"trilli/system/database/postgres"
	"trilli/system/keystore"
	"trilli/system/storage/azureblob"
	"trilli/system/storage/encryptedstore"
)

// TestLiveCreateGetPutEnd is a live integration test against the real DB +
// Azure blob store. It mints a blank session, verifies GetFile serves the
// template, PutFile round-trips edited bytes, and End deletes the working blob
// (sanitation). Skipped unless TRILLI_LIVE_TEST=1 so it doesn't run without infra.
func TestLiveCreateGetPutEnd(t *testing.T) {
	if os.Getenv("TRILLI_LIVE_TEST") != "1" {
		t.Skip("set TRILLI_LIVE_TEST=1 to run the live session round-trip")
	}
	// Install the DB-backed master-key provider (same as the daemon does at
	// startup) so the encryption decorator can unwrap per-tenant DEKs.
	keystore.Install()
	pg, err := postgres.NewClient(nil)
	if err != nil {
		t.Skipf("db unavailable: %v", err)
	}
	defer pg.Close()
	raw, err := azureblob.New(azureblob.DefaultConfig())
	if err != nil {
		t.Skipf("blob store unavailable: %v", err)
	}
	store := encryptedstore.New(raw, encryptedstore.NewKeyService(pg))
	ctx := context.Background()
	svc := NewService(pg, store)

	// Mike's seeded tenant + user.
	const tenantID, userID int64 = 199683939839, 289107109562725

	sess, err := svc.Create(ctx, CreateInput{TenantID: tenantID, UserID: userID, App: "docs"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Logf("session=%s workingPath=%s", sess.SessionKey, sess.WorkingPath)
	defer func() { _ = svc.End(ctx, sess.SessionKey) }()

	// Working path must be under the hidden .office/work prefix + tenant-scoped.
	if !strings.HasPrefix(sess.WorkingPath, "tenants/199683939839/.office/work/") {
		t.Errorf("workingPath %q not in the hidden tenant scratch area", sess.WorkingPath)
	}

	// GetFile serves the blank template (non-empty).
	rc, err := svc.GetWorking(ctx, sess)
	if err != nil {
		t.Fatalf("GetWorking: %v", err)
	}
	n, err := io.Copy(io.Discard, rc)
	rc.Close()
	if err != nil || n < 100 {
		t.Fatalf("GetWorking read %d bytes (err=%v) — blank template missing", n, err)
	}
	t.Logf("GetFile: %d bytes (blank template)", n)

	// PutFile round-trips edited bytes (encrypted at rest transparently).
	edited := "FAKE-DOCX-EDIT-BYTES-FOR-ROUNDTRIP"
	if _, err := svc.PutWorking(ctx, sess, strings.NewReader(edited)); err != nil {
		t.Fatalf("PutWorking: %v", err)
	}
	rc2, err := svc.GetWorking(ctx, sess)
	if err != nil {
		t.Fatalf("GetWorking after put: %v", err)
	}
	got, err := io.ReadAll(rc2)
	rc2.Close()
	if err != nil {
		t.Fatalf("read after put: %v", err)
	}
	if string(got) != edited {
		t.Errorf("round-trip = %q, want %q", string(got), edited)
	}
	t.Logf("round-trip OK: %q", string(got))

	// End deletes the working blob (sanitation).
	if err := svc.End(ctx, sess.SessionKey); err != nil {
		t.Fatalf("End: %v", err)
	}
	if _, err := svc.GetWorking(ctx, sess); err == nil {
		t.Error("working blob still readable after End — sanitation failed")
	}
	t.Log("End: working blob + session row deleted (sanitation OK)")
}
