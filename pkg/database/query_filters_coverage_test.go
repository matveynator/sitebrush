package database

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestMarkerQueryFilterBranches(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	err := db.withSerializedConnectionFor(context.Background(), WorkloadUserUpload, func(ctx context.Context, conn *sql.DB) error {
		fixtures := []Marker{
			{
				ID: 1, DoseRate: 0.11, Date: 100, Lon: 37.60, Lat: 55.70,
				CountRate: 11, Zoom: 8, Speed: 5, TrackID: "track-a",
				Altitude: 120, AltitudeValid: true, Detector: "gm", Radiation: "gamma",
				Temperature: 21, TemperatureValid: true, Humidity: 40, HumidityValid: true,
				DeviceID: "device-a", Transport: "walk", DeviceName: "meter-a", Tube: "tube-a", Country: "DE",
			},
			{
				ID: 2, DoseRate: 0.22, Date: 200, Lon: 37.61, Lat: 55.71,
				CountRate: 22, Zoom: 8, Speed: 25, TrackID: "track-a",
				DeviceID: "device-a", Transport: "walk", DeviceName: "meter-a", Country: "DE",
			},
			{
				ID: 3, DoseRate: 0.33, Date: 300, Lon: 37.62, Lat: 55.72,
				CountRate: 33, Zoom: 8, Speed: 55, TrackID: "track-a",
				DeviceID: "device-a", Transport: "walk", DeviceName: "meter-a", Country: "DE",
			},
			{
				ID: 4, DoseRate: 0.44, Date: 400, Lon: 37.63, Lat: 55.73,
				CountRate: 44, Zoom: 8, Speed: 90, TrackID: "track-b",
			},
		}

		for _, marker := range fixtures {
			_, err := conn.ExecContext(ctx, `INSERT INTO markers (
id,doseRate,date,lon,lat,countRate,zoom,speed,trackID,
altitude,detector,radiation,temperature,humidity,device_id,transport,device_name,tube,country
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				marker.ID, marker.DoseRate, marker.Date, marker.Lon, marker.Lat,
				marker.CountRate, marker.Zoom, marker.Speed, marker.TrackID,
				nullableFloat64(marker.AltitudeValid, marker.Altitude),
				marker.Detector, marker.Radiation,
				nullableFloat64(marker.TemperatureValid, marker.Temperature),
				nullableFloat64(marker.HumidityValid, marker.Humidity),
				marker.DeviceID, marker.Transport, marker.DeviceName, marker.Tube, marker.Country)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed query marker fixtures: %v", err)
	}

	t.Run("track date continuous speed and metadata", func(t *testing.T) {
		got, err := db.GetMarkersByTrackIDZoomBoundsSpeed(
			nil,
			"track-a",
			8,
			55, 37, 56, 38,
			150, 350,
			[]SpeedRange{{Min: 20, Max: 30}, {Min: 30, Max: 60}},
			"sqlite",
		)
		if err != nil {
			t.Fatalf("track/date/speed query: %v", err)
		}
		if len(got) != 2 || got[0].ID != 2 || got[1].ID != 3 {
			t.Fatalf("track/date/speed markers = %#v", got)
		}
	})

	t.Run("track disjoint speed", func(t *testing.T) {
		got, err := db.GetMarkersByTrackIDZoomBoundsSpeed(
			context.Background(),
			"track-a",
			8,
			55, 37, 56, 38,
			0, 0,
			[]SpeedRange{{Min: 0, Max: 10}, {Min: 50, Max: 60}},
			"sqlite",
		)
		if err != nil {
			t.Fatalf("track disjoint speed query: %v", err)
		}
		if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
			t.Fatalf("track disjoint speed markers = %#v", got)
		}
		if got[0].Altitude != 120 || got[0].Detector != "gm" || got[0].Radiation != "gamma" ||
			got[0].Temperature != 21 || got[0].Humidity != 40 ||
			got[0].DeviceID != "device-a" || got[0].DeviceName != "meter-a" ||
			got[0].Tube != "tube-a" || got[0].Country != "DE" {
			t.Fatalf("full marker metadata = %#v", got[0])
		}
	})

	t.Run("zoom date filters", func(t *testing.T) {
		got, err := db.GetMarkersByZoomBoundsSpeed(
			context.Background(),
			8,
			55, 37, 56, 38,
			150, 300,
			[]SpeedRange{{Min: 20, Max: 60}},
			"sqlite",
		)
		if err != nil {
			t.Fatalf("zoom/date/speed query: %v", err)
		}
		if len(got) != 2 || got[0].ID != 2 || got[1].ID != 3 {
			t.Fatalf("zoom/date/speed markers = %#v", got)
		}
	})

	t.Run("query error", func(t *testing.T) {
		_, err := db.queryMarkers(context.Background(), "this is not valid SQL", nil, "sqlite", WorkloadWebRead)
		if err == nil || !strings.Contains(err.Error(), "query markers") {
			t.Fatalf("invalid query error = %v", err)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := db.queryMarkers(ctx, "1=1", nil, "sqlite", WorkloadWebRead)
		if err == nil {
			t.Fatal("cancelled query unexpectedly succeeded")
		}
	})
}
