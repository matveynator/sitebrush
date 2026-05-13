package geoip

import "testing"

func TestParseDBIPCSVRecord(t *testing.T) {
	row, ok := parseDBIPCSVRecord([]string{"8.8.8.0", "8.8.8.255", "NA", "US", "California", "Mountain View", "37.4229", "-122.085"})
	if !ok {
		t.Fatalf("parseDBIPCSVRecord returned false")
	}
	if row.ipStart != 134744064 || row.ipEnd != 134744319 {
		t.Fatalf("range = %d-%d, want 134744064-134744319", row.ipStart, row.ipEnd)
	}
	if row.countryCode != "US" || row.region != "California" || row.city != "Mountain View" {
		t.Fatalf("location = %s/%s/%s", row.countryCode, row.region, row.city)
	}
	if row.latitude != 37.4229 || row.longitude != -122.085 {
		t.Fatalf("coordinates = %f,%f", row.latitude, row.longitude)
	}
}

func TestPrivateAndIPv6AddressesDoNotTriggerLocalLookup(t *testing.T) {
	for _, rawIP := range []string{"127.0.0.1", "10.0.0.5", "192.168.1.20", "::1", "2001:4860:4860::8888"} {
		if isPublicIPv4(rawIP) {
			t.Fatalf("%s treated as public IPv4", rawIP)
		}
	}
	if !isPublicIPv4("8.8.8.8") {
		t.Fatalf("8.8.8.8 was not treated as public IPv4")
	}
}
