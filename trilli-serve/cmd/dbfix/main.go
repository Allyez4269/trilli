// Temporary one-off: inspect + reset polluted Productivity zoom prefs (saved by
// the broken-button era), so resolution falls back to the tenant default.
package main

import (
	"context"
	"fmt"

	"trilli/system/database/postgres"
)

func main() {
	c, err := postgres.NewClient(nil)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	fmt.Println("=== tenant_productivity_settings ===")
	tr, err := c.QueryContext(ctx, `SELECT tenant_id, enabled, array_to_string(allowlist,','), default_zoom FROM tenant_productivity_settings`)
	if err != nil {
		panic(err)
	}
	for tr.Next() {
		var t int64
		var e bool
		var a string
		var dz int
		_ = tr.Scan(&t, &e, &a, &dz)
		fmt.Printf("  tenant=%d enabled=%v allowlist=[%s] default_zoom=%d\n", t, e, a, dz)
	}
	tr.Close()

	fmt.Println("=== user_productivity_prefs key=zoom (BEFORE) ===")
	ur, err := c.QueryContext(ctx, `SELECT user_id, value FROM user_productivity_prefs WHERE key='zoom'`)
	if err != nil {
		panic(err)
	}
	for ur.Next() {
		var u int64
		var v string
		_ = ur.Scan(&u, &v)
		fmt.Printf("  user=%d zoom=%s\n", u, v)
	}
	ur.Close()

	res, err := c.ExecContext(ctx, `DELETE FROM user_productivity_prefs WHERE key='zoom'`)
	if err != nil {
		panic(err)
	}
	n, _ := res.RowsAffected()
	fmt.Printf("=== deleted %d stale zoom pref(s) → now resolves to tenant default ===\n", n)
}
