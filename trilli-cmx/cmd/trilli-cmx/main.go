// Trilli CMX — the Customer Management (CRM) operator console for Trilli.com
// (see SPEC.md). A Go net/http monolith with an embedded React SPA, behind
// nginx (CF TLS) as the trilli-cmx systemd daemon on :9260.
//
// Subcommands:
//
//	trilli-cmx [serve]                 run the HTTP server (default)
//	trilli-cmx migrate up              apply pending CMX migrations
//	trilli-cmx migrate down <n>        roll back n CMX migrations
//	trilli-cmx migrate version         print the current CMX schema version
//	trilli-cmx create-operator ...     provision an operator (bootstrap)
//
// Env:
//
//	PORT                 listen port (default 9260)
//	CMX_DEBUG=1          enable DEBUG-level logging
//	APP_ENCRYPTION_KEY   master key for TOTP-secret encryption (shared w/ app)
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"trilli-cmx/system/admin"
	"trilli-cmx/system/appadmin"
	"trilli-cmx/system/catalog"
	"trilli-cmx/system/comp"
	"trilli-cmx/system/database/migrations"
	"trilli-cmx/system/database/postgres"
	"trilli-cmx/system/directory"
	"trilli-cmx/system/infra"
	"trilli-cmx/system/keystore"
	"trilli-cmx/system/logging"
	"trilli-cmx/system/operators"
	"trilli-cmx/system/qserve"
	"trilli-cmx/system/reports"
	"trilli-cmx/system/revenue"
	"trilli-cmx/system/server"
	"trilli-cmx/system/support"
	"trilli-cmx/system/tls"
)

const (
	defaultPort = "9260"
	serviceName = "trilli-cmx"
	// migrationsRoot is the embedded FS subdirectory holding CMX migration SQL.
	migrationsRoot = "."
)

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func main() {
	logging.EnableAll()
	if os.Getenv("CMX_DEBUG") == "1" {
		logging.SetDebugLogging(true)
	}

	// Use the SAME master key as the app: unwrap it (lazily) from the shared
	// app_master_key row with the baked root key. This keeps one source of
	// truth for secret encryption — CMX operator TOTP secrets are sealed under
	// the same master key family as the app's secrets.
	keystore.Install()

	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}

	switch cmd {
	case "serve":
		runServe()
	case "migrate":
		runMigrate(args[1:])
	case "create-operator":
		runCreateOperator(args[1:])
	case "-h", "--help", "help":
		printUsage()
	default:
		logging.Error("main", "unknown command %q", cmd)
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `trilli-cmx — Trilli CMX operator console

Usage:
  trilli-cmx [serve]                 run the HTTP server (default)
  trilli-cmx migrate up              apply pending CMX migrations
  trilli-cmx migrate down <n>        roll back n CMX migrations
  trilli-cmx migrate version         print the current CMX schema version
  trilli-cmx create-operator --email <e> --name <n> --role <global|cmx> --password <p>
`)
}

// mustDB opens the shared TRILLI database or exits.
func mustDB() *postgres.Client {
	db, err := postgres.NewClient(postgres.DefaultConfig())
	if err != nil {
		logging.Fatal("main", "database connect failed: %v", err)
	}
	return db
}

// runServe applies migrations, starts qserve GeoIP, wires the operator service,
// and runs the HTTP server.
func runServe() {
	db := mustDB()
	defer db.Close()

	// Apply CMX migrations on boot (idempotent; CMX has its own version table).
	if err := db.MigrateUp(migrations.FS, migrationsRoot); err != nil {
		logging.Fatal("main", "migrations failed: %v", err)
	}

	// GeoIP service for login audit + geo-fencing (self-maintaining MMDB).
	geoSvc := qserve.NewService()
	if err := geoSvc.Initialize(); err != nil {
		logging.Error("main", "geoip init failed (geo-fencing degraded): %v", err)
	}

	opSvc := operators.NewService(db, operators.NewGeoResolver(geoSvc))
	opHandler := operators.NewHandler(opSvc)

	dirSvc := directory.NewService(db)
	appAdmin := appadmin.NewClient(db)
	dirHandler := directory.NewHandler(dirSvc, opSvc, opHandler, appAdmin)

	catalogHandler := catalog.NewHandler(catalog.NewService(db), opHandler, opSvc, appAdmin)
	compHandler := comp.NewHandler(comp.NewService(db), opHandler, opSvc, appAdmin)
	revenueHandler := revenue.NewHandler(revenue.NewService(db), opHandler, opSvc, appAdmin)
	supportHandler := support.NewHandler(support.NewService(db), opHandler, opSvc, appAdmin)
	infraHandler := infra.NewHandler(infra.NewService(db, geoSvc.Ready, "db-ip MMDB (data/geolocation)"), opHandler, opSvc, appAdmin)
	adminHandler := admin.NewHandler(admin.NewService(db), opHandler, opSvc)
	reportsHandler := reports.NewHandler(reports.NewService(db), opHandler)

	port := env("PORT", defaultPort)

	// TLS: terminate HTTPS in-process using our wildcard certificate
	// (system/tls). Cert paths are baked in (~/certs) — no environment
	// variables — so CMX serves HTTPS directly on its own IP.
	tlsMgr, tlsErr := tls.New(tls.DefaultConfig())
	if tlsErr != nil {
		logging.Fatal("main", "TLS init failed: %v", tlsErr)
	}

	srv := server.New(server.Config{
		Addr:        "127.0.0.1:" + port,
		TLSConfig:   tlsMgr.TLSConfig(),
		ServiceName: serviceName,
		Operators:   opHandler,
		Directory:   dirHandler,
		Catalog:     catalogHandler,
		Comp:        compHandler,
		Revenue:     revenueHandler,
		Support:     supportHandler,
		Infra:       infraHandler,
		Admin:       adminHandler,
		Reports:     reportsHandler,
	})

	if err := srv.ListenAndServe(); err != nil {
		logging.Fatal("main", "server stopped: %v", err)
	}
}

func runMigrate(args []string) {
	db := mustDB()
	defer db.Close()

	sub := "up"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "up":
		if err := db.MigrateUp(migrations.FS, migrationsRoot); err != nil {
			logging.Fatal("main", "migrate up failed: %v", err)
		}
		fmt.Println("CMX migrations applied.")
	case "down":
		n := 1
		if len(args) > 1 {
			if _, err := fmt.Sscanf(args[1], "%d", &n); err != nil {
				logging.Fatal("main", "invalid step count %q", args[1])
			}
		}
		if err := db.MigrateDown(migrations.FS, migrationsRoot, n); err != nil {
			logging.Fatal("main", "migrate down failed: %v", err)
		}
		fmt.Printf("Rolled back %d CMX migration(s).\n", n)
	case "version":
		v, dirty, err := db.MigrateVersion(migrations.FS, migrationsRoot)
		if err != nil {
			logging.Fatal("main", "migrate version failed: %v", err)
		}
		fmt.Printf("CMX schema version: %d (dirty=%v)\n", v, dirty)
	default:
		logging.Fatal("main", "unknown migrate subcommand %q", sub)
	}
}

func runCreateOperator(args []string) {
	fs := flag.NewFlagSet("create-operator", flag.ExitOnError)
	email := fs.String("email", "", "operator email (login identifier)")
	name := fs.String("name", "", "operator display name")
	role := fs.String("role", "global", "role: global | cmx")
	password := fs.String("password", "", "initial password (12+ chars)")
	_ = fs.Parse(args)

	if *email == "" || *password == "" {
		logging.Fatal("main", "--email and --password are required")
	}

	db := mustDB()
	defer db.Close()

	// Ensure schema exists before inserting.
	if err := db.MigrateUp(migrations.FS, migrationsRoot); err != nil {
		logging.Fatal("main", "migrations failed: %v", err)
	}

	opSvc := operators.NewService(db, nil)
	op, err := opSvc.Bootstrap(context.Background(), *email, *name, *password, operators.Role(*role))
	if err != nil {
		logging.Fatal("main", "create operator failed: %v", err)
	}
	fmt.Printf("Created operator id=%d email=%s role=%s\n", op.ID, op.Email, op.Role)
	fmt.Println("On first login they will be required to enroll an authenticator app (mandatory 2FA).")
}
