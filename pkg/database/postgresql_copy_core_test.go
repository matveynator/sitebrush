package database

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type fakePostgresCopyConnection struct {
	tx       *fakePostgresCopyTransaction
	beginErr error
}

func (c *fakePostgresCopyConnection) begin(context.Context) (postgresCopyTransaction, error) {
	if c.beginErr != nil {
		return nil, c.beginErr
	}
	return c.tx, nil
}

type fakePostgresCopyTransaction struct {
	execQueries    []string
	copyBatchSizes []int
	execErrMatch   string
	execErr        error
	copyErrAt      int
	copyCalls      int
	commitErr      error
	committed      bool
	rolledBack     bool
}

func (t *fakePostgresCopyTransaction) exec(_ context.Context, query string, _ ...any) error {
	t.execQueries = append(t.execQueries, query)
	if t.execErr != nil && strings.Contains(query, t.execErrMatch) {
		return t.execErr
	}
	return nil
}

func (t *fakePostgresCopyTransaction) copyRows(_ context.Context, _ pgx.Identifier, columns []string, rows [][]interface{}) error {
	t.copyCalls++
	t.copyBatchSizes = append(t.copyBatchSizes, len(rows))
	if len(columns) != len(postgresMarkerColumns) {
		return errors.New("unexpected columns")
	}
	if t.copyErrAt > 0 && t.copyCalls == t.copyErrAt {
		return errors.New("copy failure")
	}
	return nil
}

func (t *fakePostgresCopyTransaction) commit(context.Context) error {
	if t.commitErr != nil {
		return t.commitErr
	}
	t.committed = true
	return nil
}

func (t *fakePostgresCopyTransaction) rollback(context.Context) {
	t.rolledBack = true
}

func postgresCopyTestMarkers() []Marker {
	return []Marker{
		{
			DoseRate: 0.1, Date: 1, Lon: 2, Lat: 3, CountRate: 4, Zoom: 5, Speed: 6, TrackID: "a",
			Altitude: 10, AltitudeValid: true, Detector: "d1", Radiation: "g",
			Temperature: 20, TemperatureValid: true, Humidity: 30, HumidityValid: true,
		},
		{
			DoseRate: 0.2, Date: 2, Lon: 3, Lat: 4, CountRate: 5, Zoom: 6, Speed: 7, TrackID: "b",
			Detector: "d2",
		},
		{DoseRate: 0.3, Date: 3, Lon: 4, Lat: 5, CountRate: 6, Zoom: 7, Speed: 8, TrackID: "c"},
	}
}

func TestPostgreSQLCopyCoreSuccess(t *testing.T) {
	tx := &fakePostgresCopyTransaction{}
	conn := &fakePostgresCopyConnection{tx: tx}
	markers := postgresCopyTestMarkers()[:2]

	if err := runPostgreSQLCopy(context.Background(), conn, markers); err != nil {
		t.Fatalf("postgres copy success: %v", err)
	}
	if !tx.committed || tx.rolledBack {
		t.Fatalf("transaction committed=%v rolledBack=%v", tx.committed, tx.rolledBack)
	}
	if len(tx.execQueries) != 2 {
		t.Fatalf("exec query count = %d, want create+merge", len(tx.execQueries))
	}
	if len(tx.copyBatchSizes) != 1 || tx.copyBatchSizes[0] != 2 {
		t.Fatalf("copy batch sizes = %#v", tx.copyBatchSizes)
	}

	rows := markerRowsForPostgreSQL(markers)
	if len(rows) != 2 || len(rows[0]) != 13 {
		t.Fatalf("postgres marker rows shape = %d x %d", len(rows), len(rows[0]))
	}
	if rows[0][8] != float64(10) || rows[1][8] != nil {
		t.Fatalf("nullable altitude mapping = %#v / %#v", rows[0][8], rows[1][8])
	}
	if !strings.Contains(postgresTempTableSQL("temp_x"), "CREATE TEMP TABLE temp_x") {
		t.Fatal("temp table SQL missing table name")
	}
	if !strings.Contains(postgresMergeTempSQL("temp_x"), "FROM temp_x") {
		t.Fatal("merge SQL missing table name")
	}
}

func TestPostgreSQLCopyCoreFailuresRollback(t *testing.T) {
	sentinel := errors.New("sentinel")

	t.Run("begin", func(t *testing.T) {
		err := runPostgreSQLCopy(context.Background(), &fakePostgresCopyConnection{beginErr: sentinel}, postgresCopyTestMarkers()[:1])
		if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "begin postgres transaction") {
			t.Fatalf("begin error = %v", err)
		}
	})

	t.Run("create", func(t *testing.T) {
		tx := &fakePostgresCopyTransaction{execErrMatch: "CREATE TEMP TABLE", execErr: sentinel}
		err := runPostgreSQLCopy(context.Background(), &fakePostgresCopyConnection{tx: tx}, postgresCopyTestMarkers()[:1])
		if !errors.Is(err, sentinel) || !tx.rolledBack {
			t.Fatalf("create error=%v rolledBack=%v", err, tx.rolledBack)
		}
	})

	t.Run("copy", func(t *testing.T) {
		tx := &fakePostgresCopyTransaction{copyErrAt: 1}
		err := runPostgreSQLCopy(context.Background(), &fakePostgresCopyConnection{tx: tx}, postgresCopyTestMarkers()[:1])
		if err == nil || !strings.Contains(err.Error(), "copy markers") || !tx.rolledBack {
			t.Fatalf("copy error=%v rolledBack=%v", err, tx.rolledBack)
		}
	})

	t.Run("merge", func(t *testing.T) {
		tx := &fakePostgresCopyTransaction{execErrMatch: "INSERT INTO markers", execErr: sentinel}
		err := runPostgreSQLCopy(context.Background(), &fakePostgresCopyConnection{tx: tx}, postgresCopyTestMarkers()[:1])
		if !errors.Is(err, sentinel) || !tx.rolledBack {
			t.Fatalf("merge error=%v rolledBack=%v", err, tx.rolledBack)
		}
	})

	t.Run("commit", func(t *testing.T) {
		tx := &fakePostgresCopyTransaction{commitErr: sentinel}
		err := runPostgreSQLCopy(context.Background(), &fakePostgresCopyConnection{tx: tx}, postgresCopyTestMarkers()[:1])
		if !errors.Is(err, sentinel) || !tx.rolledBack {
			t.Fatalf("commit error=%v rolledBack=%v", err, tx.rolledBack)
		}
	})
}

func TestPostgreSQLCopyBatchedCore(t *testing.T) {
	markers := postgresCopyTestMarkers()
	progress := make(chan MarkerBatchProgress, 4)
	tx := &fakePostgresCopyTransaction{}

	if err := runPostgreSQLCopyBatched(context.Background(), &fakePostgresCopyConnection{tx: tx}, markers, 2, progress); err != nil {
		t.Fatalf("batched postgres copy: %v", err)
	}
	if !tx.committed || tx.rolledBack {
		t.Fatalf("batched committed=%v rolledBack=%v", tx.committed, tx.rolledBack)
	}
	if len(tx.copyBatchSizes) != 2 || tx.copyBatchSizes[0] != 2 || tx.copyBatchSizes[1] != 1 {
		t.Fatalf("batched copy sizes = %#v", tx.copyBatchSizes)
	}

	first := <-progress
	second := <-progress
	if first.Total != 3 || first.Done != 2 || first.Batch != 2 || first.Mode != "copy" {
		t.Fatalf("first copy progress = %#v", first)
	}
	if second.Total != 3 || second.Done != 3 || second.Batch != 1 || second.Mode != "copy" {
		t.Fatalf("second copy progress = %#v", second)
	}
}

func TestPostgreSQLCopyBatchedFailureBranches(t *testing.T) {
	markers := postgresCopyTestMarkers()
	sentinel := errors.New("sentinel")

	tests := []struct {
		name string
		tx   *fakePostgresCopyTransaction
	}{
		{"create", &fakePostgresCopyTransaction{execErrMatch: "CREATE TEMP TABLE", execErr: sentinel}},
		{"copy", &fakePostgresCopyTransaction{copyErrAt: 1}},
		{"merge", &fakePostgresCopyTransaction{execErrMatch: "INSERT INTO markers", execErr: sentinel}},
		{"truncate", &fakePostgresCopyTransaction{execErrMatch: "TRUNCATE", execErr: sentinel}},
		{"commit", &fakePostgresCopyTransaction{commitErr: sentinel}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := runPostgreSQLCopyBatched(context.Background(), &fakePostgresCopyConnection{tx: test.tx}, markers, 2, nil)
			if err == nil || !test.tx.rolledBack {
				t.Fatalf("%s error=%v rolledBack=%v", test.name, err, test.tx.rolledBack)
			}
		})
	}

	err := runPostgreSQLCopyBatched(context.Background(), &fakePostgresCopyConnection{beginErr: sentinel}, markers, 2, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("batched begin error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tx := &fakePostgresCopyTransaction{}
	err = runPostgreSQLCopyBatched(ctx, &fakePostgresCopyConnection{tx: tx}, markers, 2, nil)
	if !errors.Is(err, context.Canceled) || !tx.rolledBack {
		t.Fatalf("cancelled batched copy error=%v rolledBack=%v", err, tx.rolledBack)
	}

	defaultBatchTx := &fakePostgresCopyTransaction{}
	if err := runPostgreSQLCopyBatched(context.Background(), &fakePostgresCopyConnection{tx: defaultBatchTx}, markers[:1], 0, nil); err != nil {
		t.Fatalf("default batch copy: %v", err)
	}
	if len(defaultBatchTx.copyBatchSizes) != 1 || defaultBatchTx.copyBatchSizes[0] != 1 {
		t.Fatalf("default batch sizes = %#v", defaultBatchTx.copyBatchSizes)
	}
}

func TestPostgreSQLCopyWrappersRejectNonPostgresConnection(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	markers := postgresCopyTestMarkers()[:1]

	err := db.insertMarkersPostgreSQLCopy(nil, markers)
	if err == nil || !strings.Contains(err.Error(), "unexpected postgres driver") {
		t.Fatalf("postgres copy sqlite boundary error = %v", err)
	}

	err = db.insertMarkersPostgreSQLCopyBatched(nil, markers, 1, nil)
	if err == nil || !strings.Contains(err.Error(), "unexpected postgres driver") {
		t.Fatalf("batched postgres copy sqlite boundary error = %v", err)
	}
}

func TestPostgreSQLCopyWrappersInputGuards(t *testing.T) {
	var nilDB *Database
	if err := nilDB.insertMarkersPostgreSQLCopy(context.Background(), postgresCopyTestMarkers()[:1]); err == nil {
		t.Fatal("nil database postgres copy did not fail")
	}
	if err := nilDB.insertMarkersPostgreSQLCopyBatched(context.Background(), postgresCopyTestMarkers()[:1], 1, nil); err == nil {
		t.Fatal("nil database batched postgres copy did not fail")
	}

	if err := nilDB.insertMarkersPostgreSQLCopy(nil, nil); err != nil {
		t.Fatalf("empty postgres copy = %v", err)
	}
	if err := nilDB.insertMarkersPostgreSQLCopyBatched(nil, nil, 0, nil); err != nil {
		t.Fatalf("empty batched postgres copy = %v", err)
	}
}
