-- Make workspaces unlimited on every plan. The per-plan cap was a real gate
-- (workspaces.Service.checkWorkspaceLimit refuses creation at HTTP 409 once an
-- account hits plans.max_workspaces); NULL means unlimited.
UPDATE plans SET max_workspaces = NULL;
