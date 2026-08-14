package azureblob

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"testing"
)

// TestBlockStagingLive exercises the real Azure block-blob stage→commit→get→abort
// path against the live storage account. Opt-in (writes a tiny ephemeral test
// blob, then deletes it) — set AZURE_LIVE_TEST=1 to run:
//
//	AZURE_LIVE_TEST=1 go test ./system/storage/azureblob/ -run TestBlockStagingLive -v
func TestBlockStagingLive(t *testing.T) {
	if os.Getenv("AZURE_LIVE_TEST") == "" {
		t.Skip("set AZURE_LIVE_TEST=1 to run the live Azure block-staging test")
	}
	store, err := New(DefaultConfig())
	if err != nil {
		t.Skipf("azure unavailable: %v", err)
	}
	ctx := context.Background()
	blobPath := "tenants/0/_test_blockstaging"
	defer store.Delete(ctx, blobPath)

	bid := func(i int) string {
		return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("blk-%09d", i)))
	}

	// Stage two blocks OUT of order; commit them IN order — the committed blob
	// must be the ordered concatenation regardless of staging order.
	if _, err := store.StageBlock(ctx, blobPath, bid(1), bytes.NewReader([]byte("world!"))); err != nil {
		t.Fatalf("stage block 1: %v", err)
	}
	if _, err := store.StageBlock(ctx, blobPath, bid(0), bytes.NewReader([]byte("hello "))); err != nil {
		t.Fatalf("stage block 0: %v", err)
	}
	if err := store.CommitBlocks(ctx, blobPath, []string{bid(0), bid(1)}); err != nil {
		t.Fatalf("commit: %v", err)
	}

	rc, err := store.Get(ctx, blobPath)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != "hello world!" {
		t.Fatalf("committed blob = %q, want %q", got, "hello world!")
	}
	if err := store.Delete(ctx, blobPath); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Abort: stage a block, abort, and confirm nothing remains committed.
	if _, err := store.StageBlock(ctx, blobPath, bid(0), bytes.NewReader([]byte("orphan"))); err != nil {
		t.Fatalf("stage for abort: %v", err)
	}
	if err := store.AbortBlocks(ctx, blobPath); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if rc, err := store.Get(ctx, blobPath); err == nil {
		rc.Close()
		t.Fatal("expected no committed blob after AbortBlocks")
	}
}
