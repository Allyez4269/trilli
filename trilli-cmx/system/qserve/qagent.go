package qserve

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"trilli-cmx/system/logging"
)

const (
	dbFileNew = "openengine-ip_NEW.mmdb"
	dbFileOld = "openengine-ip_OLD.mmdb"
)

// apiURL is the db-ip.com account download endpoint for the ip-to-location-isp
// database (the URL embeds the account's access key, so it is configuration,
// not code). Set TRILLI_DBIP_URL; when unset, automatic download/refresh is
// disabled and qserve serves whatever database file is already on disk.
var apiURL = os.Getenv("TRILLI_DBIP_URL")

// DBIPResponse represents the API response structure
type DBIPResponse struct {
	CSV struct {
		URL     string `json:"url"`
		Name    string `json:"name"`
		Date    string `json:"date"`
		Size    int64  `json:"size"`
		Rows    int    `json:"rows"`
		MD5Sum  string `json:"md5sum"`
		SHA1Sum string `json:"sha1sum"`
		Version int    `json:"version"`
	} `json:"csv"`
	MMDB struct {
		URL     string `json:"url"`
		Name    string `json:"name"`
		Date    string `json:"date"`
		Size    int64  `json:"size"`
		Rows    int    `json:"rows"`
		MD5Sum  string `json:"md5sum"`
		SHA1Sum string `json:"sha1sum"`
	} `json:"mmdb"`
}

// QAgent monitors and maintains the GeoIP database file
type QAgent struct {
	dataDir string
	stop    chan struct{}
}

// NewQAgent creates a new QAgent instance
func NewQAgent(dataDir string) *QAgent {
	// Ensure data directory exists with proper permissions
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		logging.Error(packageName, "Failed to create data directory: %v", err)
	}

	// Set directory permissions to allow service user
	if err := os.Chmod(dataDir, 0755); err != nil {
		logging.Error(packageName, "Failed to set data directory permissions: %v", err)
	}

	return &QAgent{
		dataDir: dataDir,
		stop:    make(chan struct{}),
	}
}

// EnsureDB downloads the database if it's missing. Safe to call synchronously at
// startup — it's a no-op when the file already exists.
func (qa *QAgent) EnsureDB() error {
	return qa.checkAndUpdateDB(false)
}

// Start begins monitoring the data directory
func (qa *QAgent) Start() {
	logging.Info(packageName, "Starting QAgent monitoring of %s", qa.dataDir)

	// Start minute ticker for missing file checks
	minuteTicker := time.NewTicker(1 * time.Minute)

	// Calculate time until next daily update
	now := time.Now().UTC()
	nextUpdate := time.Date(now.Year(), now.Month(), now.Day(), dailyUpdateHour, dailyUpdateMinute, 0, 0, time.UTC)
	if now.After(nextUpdate) {
		nextUpdate = nextUpdate.Add(24 * time.Hour)
	}

	// Start daily update timer
	dailyTimer := time.NewTimer(time.Until(nextUpdate))

	go func() {
		for {
			select {
			case <-minuteTicker.C:
				// Check for missing file
				if err := qa.checkAndUpdateDB(false); err != nil {
					logging.Error(packageName, "Error checking/updating database: %v", err)
				}

			case <-dailyTimer.C:
				// Perform daily update
				logging.Info(packageName, "Starting scheduled daily update at %s UTC", time.Now().UTC().Format("15:04"))
				if err := qa.checkAndUpdateDB(true); err != nil {
					logging.Error(packageName, "Error performing daily update: %v", err)
				}

				// Reset timer for next day
				dailyTimer.Reset(24 * time.Hour)

			case <-qa.stop:
				minuteTicker.Stop()
				dailyTimer.Stop()
				return
			}
		}
	}()
}

// Stop halts the monitoring and waits for pending operations
func (qa *QAgent) Stop() {
	logging.Info(packageName, "Stopping QAgent monitoring")
	close(qa.stop)

	// Give pending operations time to complete
	time.Sleep(100 * time.Millisecond)

	// Clean up any temporary files
	newPath := filepath.Join(qa.dataDir, dbFileNew)
	if _, err := os.Stat(newPath); err == nil {
		if err := os.Remove(newPath); err != nil {
			logging.Error(packageName, "Failed to clean up temporary file %s: %v", newPath, err)
		}
	}

	oldPath := filepath.Join(qa.dataDir, dbFileOld)
	if _, err := os.Stat(oldPath); err == nil {
		if err := os.Remove(oldPath); err != nil {
			logging.Error(packageName, "Failed to clean up temporary file %s: %v", oldPath, err)
		}
	}

	logging.Info(packageName, "QAgent monitoring stopped successfully")
}

// checkAndUpdateDB verifies the database file exists and updates it if needed or forced
func (qa *QAgent) checkAndUpdateDB(forceUpdate bool) error {
	dbPath := filepath.Join(qa.dataDir, dbFileName)

	// Check if file exists
	fileExists := true
	if _, err := os.Stat(dbPath); err != nil {
		fileExists = false
		logging.Info(packageName, "Database file not found at %s", dbPath)
	}

	// Skip update if file exists and no force update requested
	if fileExists && !forceUpdate {
		logging.Debug(packageName, "Database file exists and no update forced")
		return nil
	}

	// Log update reason
	if forceUpdate && fileExists {
		logging.Info(packageName, "Forcing database update at %s UTC", time.Now().UTC().Format("15:04"))
	} else {
		logging.Info(packageName, "Initiating database download")
	}

	// Step 1: Download new file directly as _NEW
	newPath := filepath.Join(qa.dataDir, dbFileNew)
	if err := qa.downloadFile(newPath); err != nil {
		return fmt.Errorf("failed to download database: %v", err)
	}

	// Step 2: Rename current to _OLD (if it exists)
	oldPath := filepath.Join(qa.dataDir, dbFileOld)
	if _, err := os.Stat(dbPath); err == nil {
		if err := os.Rename(dbPath, oldPath); err != nil {
			return fmt.Errorf("failed to rename current file to _OLD: %v", err)
		}
	}

	// Step 3: Rename _NEW to current and set permissions
	if err := os.Rename(newPath, dbPath); err != nil {
		// Try to recover by renaming _OLD back if it exists
		if _, err2 := os.Stat(oldPath); err2 == nil {
			os.Rename(oldPath, dbPath)
		}
		return fmt.Errorf("failed to rename _NEW to current: %v", err)
	}

	// Set permissions on the new current file
	if err := os.Chmod(dbPath, 0644); err != nil {
		logging.Error(packageName, "Failed to set permissions on current file: %v", err)
		// Non-fatal error, continue anyway
	}

	// Step 4: Delete _OLD
	if _, err := os.Stat(oldPath); err == nil {
		if err := os.Remove(oldPath); err != nil {
			logging.Error(packageName, "Failed to delete old database file: %v", err)
			// Non-fatal error, continue anyway
		}
	}

	logging.Info(packageName, "Successfully updated database file")
	return nil
}

// downloadFile downloads the database file from the API
func (qa *QAgent) downloadFile(downloadPath string) error {
	if apiURL == "" {
		return fmt.Errorf("TRILLI_DBIP_URL not set — automatic GeoIP database download disabled")
	}
	// Step 1: Get download URL from API
	logging.Info(packageName, "Fetching database info from API: %s", apiURL)
	resp, err := http.Get(apiURL)
	if err != nil {
		return fmt.Errorf("failed to fetch API URL: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API returned bad status: %s", resp.Status)
	}

	// Parse API response
	var apiResp DBIPResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return fmt.Errorf("failed to parse API response: %v", err)
	}

	// Step 2: Download the actual database file
	logging.Info(packageName, "Downloading database from: %s", apiResp.MMDB.URL)
	dbResp, err := http.Get(apiResp.MMDB.URL)
	if err != nil {
		return fmt.Errorf("failed to download database: %v", err)
	}
	defer dbResp.Body.Close()

	if dbResp.StatusCode != http.StatusOK {
		return fmt.Errorf("database download returned bad status: %s", dbResp.Status)
	}

	// Create output file with proper permissions
	out, err := os.OpenFile(downloadPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create download file: %v", err)
	}
	defer out.Close()

	// Set file permissions to allow service user
	if err := os.Chmod(downloadPath, 0644); err != nil {
		return fmt.Errorf("failed to set file permissions: %v", err)
	}

	// Step 3: Decompress gzip stream while downloading
	gzReader, err := gzip.NewReader(dbResp.Body)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()

	// Copy decompressed data to file
	if _, err := io.Copy(out, gzReader); err != nil {
		return fmt.Errorf("failed to write decompressed data: %v", err)
	}

	return nil
}
