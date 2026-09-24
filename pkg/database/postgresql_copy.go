package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

var postgresMarkerColumns = []string{
	"doseRate", "date", "lon", "lat", "countRate", "zoom", "speed", "trackID",
	"altitude", "detector", "radiation", "temperature", "humidity",
}

type postgresCopyConnection interface {
	begin(context.Context) (postgresCopyTransaction, error)
}

type postgresCopyTransaction interface {
	exec(context.Context, string, ...any) error
	copyRows(context.Context, pgx.Identifier, []string, [][]interface{}) error
	commit(context.Context) error
	rollback(context.Context)
}

type pgxPostgresCopyConnection struct {
	conn *pgx.Conn
}

func (c pgxPostgresCopyConnection) begin(ctx context.Context) (postgresCopyTransaction, error) {
	tx, err := c.conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	return pgxPostgresCopyTransaction{tx: tx}, nil
}

type pgxPostgresCopyTransaction struct {
	tx pgx.Tx
}

func (t pgxPostgresCopyTransaction) exec(ctx context.Context, query string, args ...any) error {
	_, err := t.tx.Exec(ctx, query, args...)
	return err
}

func (t pgxPostgresCopyTransaction) copyRows(ctx context.Context, table pgx.Identifier, columns []string, rows [][]interface{}) error {
	_, err := t.tx.CopyFrom(ctx, table, columns, pgx.CopyFromRows(rows))
	return err
}

func (t pgxPostgresCopyTransaction) commit(ctx context.Context) error {
	return t.tx.Commit(ctx)
}

func (t pgxPostgresCopyTransaction) rollback(ctx context.Context) {
	_ = t.tx.Rollback(ctx)
}

func markerRowsForPostgreSQL(markers []Marker) [][]interface{} {
	rows := make([][]interface{}, 0, len(markers))
	for _, m := range markers {
		rows = append(rows, []interface{}{
			m.DoseRate, m.Date, m.Lon, m.Lat,
			m.CountRate, m.Zoom, m.Speed, m.TrackID,
			nullableFloat64(m.AltitudeValid, m.Altitude),
			m.Detector, m.Radiation,
			nullableFloat64(m.TemperatureValid, m.Temperature),
			nullableFloat64(m.HumidityValid, m.Humidity),
		})
	}
	return rows
}

func postgresTempTableSQL(tempTable string) string {
	return fmt.Sprintf(`CREATE TEMP TABLE %s (
 doseRate DOUBLE PRECISION,
 date BIGINT,
 lon DOUBLE PRECISION,
 lat DOUBLE PRECISION,
 countRate DOUBLE PRECISION,
 zoom INT,
 speed DOUBLE PRECISION,
 trackID TEXT,
 altitude DOUBLE PRECISION,
 detector TEXT,
 radiation TEXT,
 temperature DOUBLE PRECISION,
 humidity DOUBLE PRECISION
 ) ON COMMIT DROP`, tempTable)
}

func postgresMergeTempSQL(tempTable string) string {
	return fmt.Sprintf(`INSERT INTO markers
 (doseRate,date,lon,lat,countRate,zoom,speed,trackID,altitude,detector,radiation,temperature,humidity)
 SELECT doseRate,date,lon,lat,countRate,zoom,speed,trackID,altitude,detector,radiation,temperature,humidity FROM %s
 ON CONFLICT ON CONSTRAINT markers_unique DO NOTHING`, tempTable)
}

func runPostgreSQLCopy(ctx context.Context, conn postgresCopyConnection, chunk []Marker) error {
	tx, err := conn.begin(ctx)
	if err != nil {
		return fmt.Errorf("begin postgres transaction: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			tx.rollback(ctx)
		}
	}()

	tempTable := fmt.Sprintf("temp_markers_%d", time.Now().UnixNano())
	if err := tx.exec(ctx, postgresTempTableSQL(tempTable)); err != nil {
		return fmt.Errorf("create temp table: %w", err)
	}

	rows := markerRowsForPostgreSQL(chunk)
	if err := tx.copyRows(ctx, pgx.Identifier{tempTable}, postgresMarkerColumns, rows); err != nil {
		return fmt.Errorf("copy markers into temp table: %w", err)
	}

	if err := tx.exec(ctx, postgresMergeTempSQL(tempTable)); err != nil {
		return fmt.Errorf("merge temp markers: %w", err)
	}

	if err := tx.commit(ctx); err != nil {
		return fmt.Errorf("commit temp markers: %w", err)
	}
	committed = true
	return nil
}

func runPostgreSQLCopyBatched(
	ctx context.Context,
	conn postgresCopyConnection,
	markers []Marker,
	batch int,
	progress chan<- MarkerBatchProgress,
) error {
	if batch <= 0 {
		batch = 500
	}

	tx, err := conn.begin(ctx)
	if err != nil {
		return fmt.Errorf("begin postgres transaction: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			tx.rollback(ctx)
		}
	}()

	tempTable := fmt.Sprintf("temp_markers_%d", time.Now().UnixNano())
	if err := tx.exec(ctx, postgresTempTableSQL(tempTable)); err != nil {
		return fmt.Errorf("create temp table: %w", err)
	}

	mergeSQL := postgresMergeTempSQL(tempTable)
	truncateSQL := fmt.Sprintf("TRUNCATE %s", tempTable)
	total := len(markers)
	done := 0

	for start := 0; start < len(markers); start += batch {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		end := start + batch
		if end > len(markers) {
			end = len(markers)
		}
		chunk := markers[start:end]
		rows := markerRowsForPostgreSQL(chunk)
		if len(rows) == 0 {
			continue
		}

		chunkStart := time.Now()
		if err := tx.copyRows(ctx, pgx.Identifier{tempTable}, postgresMarkerColumns, rows); err != nil {
			return fmt.Errorf("copy markers into temp table: %w", err)
		}
		if err := tx.exec(ctx, mergeSQL); err != nil {
			return fmt.Errorf("merge temp markers: %w", err)
		}
		if err := tx.exec(ctx, truncateSQL); err != nil {
			return fmt.Errorf("reset temp table: %w", err)
		}

		done += len(chunk)
		if progress != nil {
			select {
			case progress <- MarkerBatchProgress{
				Total: total, Done: done, Batch: len(chunk), Mode: "copy", Duration: time.Since(chunkStart),
			}:
			default:
			}
		}
	}

	if err := tx.commit(ctx); err != nil {
		return fmt.Errorf("commit temp markers: %w", err)
	}
	committed = true
	return nil
}

// insertMarkersPostgreSQLCopy streams a chunk of markers into PostgreSQL using COPY to
// keep imports fast. The database/sql boundary is kept small so the COPY transaction
// can be tested without requiring a live PostgreSQL server.
func (db *Database) insertMarkersPostgreSQLCopy(ctx context.Context, chunk []Marker) error {
	if len(chunk) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || db.DB == nil {
		return fmt.Errorf("database unavailable")
	}

	conn, err := db.DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open postgres connection: %w", err)
	}
	defer conn.Close()

	return conn.Raw(func(driverConn any) error {
		direct, ok := driverConn.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("unexpected postgres driver %T", driverConn)
		}
		return runPostgreSQLCopy(ctx, pgxPostgresCopyConnection{conn: direct.Conn()}, chunk)
	})
}

// insertMarkersPostgreSQLCopyBatched streams all markers through one PostgreSQL COPY
// transaction. The transaction logic lives behind a narrow adapter so failure and
// progress paths remain testable without network infrastructure.
func (db *Database) insertMarkersPostgreSQLCopyBatched(
	ctx context.Context,
	markers []Marker,
	batch int,
	progress chan<- MarkerBatchProgress,
) error {
	if len(markers) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || db.DB == nil {
		return fmt.Errorf("database unavailable")
	}

	conn, err := db.DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open postgres connection: %w", err)
	}
	defer conn.Close()

	return conn.Raw(func(driverConn any) error {
		direct, ok := driverConn.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("unexpected postgres driver %T", driverConn)
		}
		return runPostgreSQLCopyBatched(ctx, pgxPostgresCopyConnection{conn: direct.Conn()}, markers, batch, progress)
	})
}
