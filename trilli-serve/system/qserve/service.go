package qserve

import (
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"time"

	"trilli/system/logging"

	"github.com/oschwald/maxminddb-golang"
)

// Service provides GeoIP lookup functionality
type Service struct {
	mmdbDB     *maxminddb.Reader // current active database
	mmdbMutex  sync.RWMutex      // protects database swaps
	lookupPool chan struct{}     // bounds concurrent lookups
	stopChan   chan struct{}
	qagent     *QAgent
}

// NewService creates a new GeoIP service instance. Configuration (data dir, API
// key) is baked into the package constants.
func NewService() *Service {
	return &Service{
		stopChan:   make(chan struct{}),
		qagent:     NewQAgent(dataDir),
		lookupPool: make(chan struct{}, maxConcurrentLookups),
	}
}

// Initialize starts the background DB maintenance and loads the MaxMind DB.
// Non-blocking and non-fatal: a missing DB or a slow first download never
// blocks or crashes app startup — the endpoint reports "not ready" until the
// file lands, and the periodic refresh picks it up.
func (s *Service) Initialize() error {
	logging.Info(packageName, "Initializing GeoIP service (data dir: %s)", dataDir)

	// Background monitor: minute-checks for a missing file + daily refresh.
	s.qagent.Start()

	// Ensure + load in the background so the first (possibly large) download
	// doesn't stall boot. On a warm boot the file already exists and this is
	// effectively instant.
	go func() {
		if err := s.qagent.EnsureDB(); err != nil {
			logging.Error(packageName, "Initial database ensure failed: %v", err)
		}
		if err := s.loadMMDB(); err != nil {
			logging.Error(packageName, "Initial MMDB load failed (will retry on refresh): %v", err)
		} else {
			logging.Info(packageName, "GeoIP database loaded")
		}
	}()

	// Periodic reload from disk (picks up freshly-downloaded files).
	go s.startMMDBRefresh()

	logging.Info(packageName, "GeoIP service initialized")
	return nil
}

// loadMMDB loads or reloads the MaxMind database
func (s *Service) loadMMDB() error {
	dbPath := filepath.Join(dataDir, dbFileName)

	// Load new database first
	newMMDB, err := maxminddb.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open MMDB file: %w", err)
	}

	// Acquire write lock only for the quick pointer swap
	s.mmdbMutex.Lock()
	oldMMDB := s.mmdbDB
	s.mmdbDB = newMMDB
	s.mmdbMutex.Unlock()

	// Close old database after releasing lock
	if oldMMDB != nil {
		go func(db *maxminddb.Reader) {
			if err := db.Close(); err != nil {
				logging.Error(packageName, "Failed to close old MMDB: %v", err)
			}
		}(oldMMDB)
	}

	return nil
}

// startMMDBRefresh periodically refreshes the MaxMind database
func (s *Service) startMMDBRefresh() {
	ticker := time.NewTicker(defaultMMDBRefreshMinutes * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			logging.Debug(packageName, "Refreshing MaxMind database")
			if err := s.loadMMDB(); err != nil {
				logging.Error(packageName, "Failed to refresh MMDB: %v", err)
			} else {
				logging.Debug(packageName, "MaxMind database refreshed successfully")
			}
		case <-s.stopChan:
			return
		}
	}
}

// ValidateAPIKey checks if the provided API key is valid
func (s *Service) ValidateAPIKey(key string) bool {
	return key != "" && key == validAPIKey
}

// LookupIP performs a GeoIP lookup for the provided IP address. Returns an error
// (not a panic) when the database isn't loaded yet.
func (s *Service) LookupIP(ipStr string) (*GeoIPResponse, error) {
	// Acquire connection from pool
	s.lookupPool <- struct{}{}
	defer func() { <-s.lookupPool }()

	// Parse the IP address
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return nil, fmt.Errorf("invalid IP address: %s", ipStr)
	}

	// Determine IP version
	ipVersion := "IPv4"
	if ip.To4() == nil && ip.To16() != nil {
		ipVersion = "IPv6"
	}

	// Quick read lock for database pointer access
	s.mmdbMutex.RLock()
	db := s.mmdbDB
	s.mmdbMutex.RUnlock()

	if db == nil {
		return nil, fmt.Errorf("geoip database not ready")
	}

	// Perform lookup without holding lock
	var mmResp MaxMindResponse
	if err := db.Lookup(ip, &mmResp); err != nil {
		return nil, fmt.Errorf("MMDB lookup failed: %w", err)
	}

	// Check if we got any meaningful data
	if mmResp.City.GeonameID == 0 && mmResp.City.Names["en"] == "" {
		return nil, fmt.Errorf("no data found for IP: %s", ipStr)
	}

	// Construct the response
	geoResp := &GeoIPResponse{
		IP:                 ipStr,
		IPVersion:          ipVersion,
		City:               mmResp.City.Names["en"],
		CityGeonameID:      mmResp.City.GeonameID,
		ContinentCode:      mmResp.Continent.Code,
		ContinentGeonameID: mmResp.Continent.GeonameID,
		ContinentName:      mmResp.Continent.Names["en"],
		CountryGeonameID:   mmResp.Country.GeonameID,
		CountryCode:        mmResp.Country.ISOCode,
		CountryName:        mmResp.Country.Names["en"],
		IsInEU:             mmResp.Country.IsInEuropeanUnion,
		Latitude:           mmResp.Location.Latitude,
		Longitude:          mmResp.Location.Longitude,
		TimeZone:           mmResp.Location.TimeZone,
		WeatherCode:        mmResp.Location.WeatherCode,
		PostalCode:         mmResp.Postal.Code,
		Subdivisions:       extractSubdivisions(mmResp.Subdivisions),
	}

	// Add traits if available
	if mmResp.Traits != nil {
		geoResp.Traits = &TraitsJSON{
			AutonomousSystemNumber:       mmResp.Traits.AutonomousSystemNumber,
			AutonomousSystemOrganization: mmResp.Traits.AutonomousSystemOrganization,
			ConnectionType:               mmResp.Traits.ConnectionType,
			UserType:                     mmResp.Traits.UserType,
			ISP:                          mmResp.Traits.ISP,
			Organization:                 mmResp.Traits.Organization,
		}
	}

	return geoResp, nil
}

// extractSubdivisions converts MaxMind subdivisions to the JSON shape.
func extractSubdivisions(subdivisions []MaxMindSubdivision) []SubdivisionJSON {
	var result []SubdivisionJSON
	for _, subdivision := range subdivisions {
		name := subdivision.Names["en"]
		subdivisionJSON := SubdivisionJSON{
			GeonameID: subdivision.GeonameID,
			Name:      name,
		}
		if subdivision.ISOCode != "" {
			subdivisionJSON.ISOCode = subdivision.ISOCode
		}
		result = append(result, subdivisionJSON)
	}
	return result
}

// Stop gracefully shuts down the service
func (s *Service) Stop() error {
	logging.Info(packageName, "Stopping GeoIP service")

	// Stop QAgent first.
	if s.qagent != nil {
		s.qagent.Stop()
	}

	// Stop refresh goroutine and let pending operations settle.
	close(s.stopChan)
	time.Sleep(100 * time.Millisecond)

	// Close database with proper locking.
	s.mmdbMutex.Lock()
	defer s.mmdbMutex.Unlock()
	if s.mmdbDB != nil {
		if err := s.mmdbDB.Close(); err != nil {
			return fmt.Errorf("failed to close MMDB: %w", err)
		}
		s.mmdbDB = nil
	}

	logging.Info(packageName, "GeoIP service stopped")
	return nil
}
