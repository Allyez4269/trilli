package qserve

import (
	"os"
	"strings"
)

// Configuration for the GeoIP service. The lookup key and the database
// download URL come from the environment; the rest is fixed.

// validAPIKey gates the lookup endpoint (?k=). Set TRILLI_GEOIP_KEY to any
// random value (e.g. a UUID). When unset, every lookup is rejected.
var validAPIKey = strings.TrimSpace(os.Getenv("TRILLI_GEOIP_KEY"))

const (
	packageName = "qserve"

	// dataDir is relative to the process working directory (the repo root per
	// the systemd unit), so the MaxMind DB lives at data/geolocation.
	dataDir = "data/geolocation"

	// dbFileName is the active MaxMind database file inside the data dir.
	dbFileName = "openengine-ip.mmdb"

	// defaultMMDBRefreshMinutes is how often the in-memory DB is reloaded from
	// disk (picks up a freshly-downloaded file without a restart).
	defaultMMDBRefreshMinutes = 5

	// Daily refresh schedule (UTC) for fetching the newest database.
	dailyUpdateHour   = 7 // 07:00 UTC
	dailyUpdateMinute = 0

	// maxConcurrentLookups bounds in-flight lookups.
	maxConcurrentLookups = 1000
)
