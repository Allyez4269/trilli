package qserve

// GeoIPResponse represents the complete GeoIP response to be sent as JSON
type GeoIPResponse struct {
	IP                 string            `json:"ip"`
	IPVersion          string            `json:"ip_version"`
	City               string            `json:"city"`
	CityGeonameID      uint32            `json:"city_geoname_id"`
	ContinentCode      string            `json:"continent_code"`
	ContinentGeonameID uint32            `json:"continent_geoname_id"`
	ContinentName      string            `json:"continent_name"`
	CountryGeonameID   uint32            `json:"country_geoname_id"`
	CountryCode        string            `json:"country_code"`
	CountryName        string            `json:"country_name"`
	IsInEU             bool              `json:"is_in_eu"`
	Latitude           float64           `json:"latitude"`
	Longitude          float64           `json:"longitude"`
	TimeZone           string            `json:"time_zone"`
	WeatherCode        string            `json:"weather_code"`
	PostalCode         string            `json:"postal_code"`
	Subdivisions       []SubdivisionJSON `json:"subdivisions,omitempty"`
	Traits             *TraitsJSON       `json:"traits,omitempty"`
}

// SubdivisionJSON represents the administrative subdivisions with only English names
type SubdivisionJSON struct {
	GeonameID uint32 `json:"geoname_id"`
	ISOCode   string `json:"iso_code,omitempty"`
	Name      string `json:"name"`
}

// TraitsJSON represents the network traits
type TraitsJSON struct {
	AutonomousSystemNumber       uint32 `json:"autonomous_system_number,omitempty"`
	AutonomousSystemOrganization string `json:"autonomous_system_organization,omitempty"`
	ConnectionType               string `json:"connection_type,omitempty"`
	UserType                     string `json:"user_type,omitempty"`
	ISP                          string `json:"isp,omitempty"`
	Organization                 string `json:"organization,omitempty"`
}

// MaxMindResponse represents the entire data structure from MMDB
type MaxMindResponse struct {
	City         MaxMindCity          `maxminddb:"city"`
	Continent    MaxMindContinent     `maxminddb:"continent"`
	Country      MaxMindCountry       `maxminddb:"country"`
	Location     MaxMindLocation      `maxminddb:"location"`
	Postal       MaxMindPostal        `maxminddb:"postal"`
	Subdivisions []MaxMindSubdivision `maxminddb:"subdivisions"`
	Traits       *MaxMindTraits       `maxminddb:"traits"`
}

// MaxMindCity represents the city data from MMDB
type MaxMindCity struct {
	GeonameID uint32            `maxminddb:"geoname_id"`
	Names     map[string]string `maxminddb:"names"`
}

// MaxMindContinent represents the continent data from MMDB
type MaxMindContinent struct {
	Code      string            `maxminddb:"code"`
	GeonameID uint32            `maxminddb:"geoname_id"`
	Names     map[string]string `maxminddb:"names"`
}

// MaxMindCountry represents the country data from MMDB
type MaxMindCountry struct {
	GeonameID         uint32            `maxminddb:"geoname_id"`
	IsInEuropeanUnion bool              `maxminddb:"is_in_european_union"`
	ISOCode           string            `maxminddb:"iso_code"`
	Names             map[string]string `maxminddb:"names"`
}

// MaxMindLocation represents the location data from MMDB
type MaxMindLocation struct {
	Latitude    float64 `maxminddb:"latitude"`
	Longitude   float64 `maxminddb:"longitude"`
	TimeZone    string  `maxminddb:"time_zone"`
	WeatherCode string  `maxminddb:"weather_code"`
}

// MaxMindPostal represents the postal code data from MMDB
type MaxMindPostal struct {
	Code string `maxminddb:"code"`
}

// MaxMindSubdivision represents a subdivision from MMDB
type MaxMindSubdivision struct {
	GeonameID uint32            `maxminddb:"geoname_id"`
	ISOCode   string            `maxminddb:"iso_code"`
	Names     map[string]string `maxminddb:"names"`
}

// MaxMindTraits represents the traits from MMDB
type MaxMindTraits struct {
	AutonomousSystemNumber       uint32 `maxminddb:"autonomous_system_number"`
	AutonomousSystemOrganization string `maxminddb:"autonomous_system_organization"`
	ConnectionType               string `maxminddb:"connection_type"`
	UserType                     string `maxminddb:"user_type"`
	ISP                          string `maxminddb:"isp"`
	Organization                 string `maxminddb:"organization"`
}
