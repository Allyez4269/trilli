// Package cluster coordinates the in-binary background jobs across a cluster of
// identical replicas. Every replica schedules every janitor goroutine, but only
// ONE replica should actually do the work each cycle — otherwise customers get
// duplicate emails and we pay for duplicate Azure operations.
//
// Coordination is pure Postgres: a per-job advisory lock (pg_try_advisory_lock).
// The first node to grab the lock runs the job; the rest skip instantly. The
// lock is held on a dedicated connection for the job's duration and released
// (or auto-released by Postgres if the node dies), so a crash mid-job just means
// another node picks it up next cycle. No external scheduler or leader service.
package cluster

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"time"

	"trilli/system/database/postgres"
	"trilli/system/logging"
)

const packageName = "cluster"

// Coordinator runs jobs under cluster-wide mutual exclusion.
type Coordinator struct {
	client *postgres.Client
	nodeID string
}

// NewCoordinator builds a coordinator. nodeID identifies this replica in the
// job_runs log (hostname + pid — unique per pod/process in a cluster).
func NewCoordinator(c *postgres.Client) *Coordinator {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "node"
	}
	return &Coordinator{client: c, nodeID: fmt.Sprintf("%s/%d", host, os.Getpid())}
}

// NodeID is this replica's identifier.
func (c *Coordinator) NodeID() string { return c.nodeID }

// RunExclusive runs fn iff this node can acquire the cluster-wide lock for job.
// Returns ran=false (nil error) when another node currently holds it — the
// caller should treat that as a normal skip, not a failure. The job's outcome
// is recorded in job_runs for cross-cluster visibility.
func (c *Coordinator) RunExclusive(ctx context.Context, job string, fn func(context.Context) error) (ran bool, err error) {
	key := jobKey(job)

	// A dedicated connection: advisory locks are session-scoped, so the lock
	// and unlock must run on the same physical connection (and closing it is a
	// final safety net that releases the lock if we somehow don't).
	conn, err := c.client.GetDB().Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("cluster: acquire conn: %w", err)
	}
	defer conn.Close()

	var got bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&got); err != nil {
		return false, fmt.Errorf("cluster: try lock %q: %w", job, err)
	}
	if !got {
		return false, nil // another node owns this job right now
	}
	// Release on a background context so an already-cancelled ctx (shutdown)
	// can't leave the lock dangling on this connection before Close runs.
	defer func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)
	}()

	started := time.Now()
	runErr := fn(ctx)
	c.record(job, started, runErr)
	return true, runErr
}

// record upserts the job's last-run row. Best-effort: a logging failure must not
// mask the job's own result.
func (c *Coordinator) record(job string, started time.Time, runErr error) {
	note, ok := "ok", true
	if runErr != nil {
		note, ok = runErr.Error(), false
	}
	if len(note) > 500 {
		note = note[:500]
	}
	if _, err := c.client.Exec(`
		INSERT INTO job_runs (job, last_node, last_started_at, last_finished_at, last_ok, last_note, run_count)
		VALUES ($1, $2, $3, NOW(), $4, $5, 1)
		ON CONFLICT (job) DO UPDATE SET
		    last_node = $2, last_started_at = $3, last_finished_at = NOW(),
		    last_ok = $4, last_note = $5, run_count = job_runs.run_count + 1`,
		job, c.nodeID, started, ok, note); err != nil {
		logging.Debug(packageName, "record run %q: %v", job, err)
	}
}

// jobKey hashes a job name to a stable 64-bit advisory-lock key.
func jobKey(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return int64(h.Sum64())
}

// Key exposes the advisory-lock key for a job name (for diagnostics/tests).
func Key(name string) int64 { return jobKey(name) }
