package operators

import (
	"strings"

	"trilli-cmx/system/qserve"
)

// GeoResolver turns a login IP into a Geo (continent/country/region). Backed by
// the qserve GeoIP service; isolated behind this interface so the operator
// service stays decoupled and testable.
type GeoResolver interface {
	Locate(ip string) Geo
}

// qserveResolver adapts *qserve.Service to GeoResolver.
type qserveResolver struct{ svc *qserve.Service }

// NewGeoResolver wraps a qserve service as a GeoResolver.
func NewGeoResolver(svc *qserve.Service) GeoResolver { return &qserveResolver{svc: svc} }

func (q *qserveResolver) Locate(ip string) Geo {
	if q == nil || q.svc == nil || ip == "" {
		return Geo{}
	}
	resp, err := q.svc.LookupIP(ip)
	if err != nil || resp == nil {
		return Geo{}
	}
	region := ""
	if len(resp.Subdivisions) > 0 {
		region = resp.Subdivisions[0].Name
		if region == "" {
			region = resp.Subdivisions[0].ISOCode
		}
	}
	return Geo{
		ContinentCode: resp.ContinentCode,
		CountryCode:   resp.CountryCode,
		Region:        region,
	}
}

// geofenceAllows reports whether a login from geo is permitted given the
// operator's fence rules (SPEC §6.9). Semantics:
//   - geofence disabled OR no rules → always allowed (default unrestricted).
//   - geofence enabled with rules → fail-CLOSED: the region must resolve AND
//     match at least one rule. An unresolvable IP (e.g. private/localhost, or a
//     not-yet-ready GeoIP DB) is BLOCKED for a fenced operator, because a
//     god-mode tool must not silently drop its location guarantee.
func geofenceAllows(enabled bool, rules []GeofenceRule, geo Geo) bool {
	if !enabled || len(rules) == 0 {
		return true
	}
	if !geo.Resolved() {
		return false
	}
	for _, r := range rules {
		switch r.RegionType {
		case "continent":
			if strings.EqualFold(r.RegionCode, geo.ContinentCode) {
				return true
			}
		case "country":
			if strings.EqualFold(r.RegionCode, geo.CountryCode) {
				return true
			}
		}
	}
	return false
}

// GeofenceRule is one allowed-region entry for an operator.
type GeofenceRule struct {
	RegionType string `json:"region_type"` // "continent" | "country"
	RegionCode string `json:"region_code"`
}
