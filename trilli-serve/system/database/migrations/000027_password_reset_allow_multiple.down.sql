-- Restore the one-pending-per-user constraint. Cancel any extra pending rows
-- first so the unique index can be rebuilt.
UPDATE password_reset_requests pr SET status = 'cancelled'
 WHERE status = 'pending'
   AND id <> (
       SELECT id FROM password_reset_requests x
        WHERE x.user_id = pr.user_id AND x.status = 'pending'
        ORDER BY created_at DESC, id DESC LIMIT 1
   );

CREATE UNIQUE INDEX password_reset_pending_per_user_uq
    ON password_reset_requests (user_id)
    WHERE status = 'pending';
