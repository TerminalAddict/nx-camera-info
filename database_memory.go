package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// No temporary database, journal or SQLite sort file is used for downloaded
// snapshots. Deserialize copies the bytes into SQLite's own allocation, which
// is released when the connection closes. The original bytes can be saved on
// explicit request; their contents are never changed here.
func readMemoryReport(ctx context.Context, data []byte) (Report, error) {
	if len(data) < 100 || !bytes.Equal(data[:16], []byte("SQLite format 3\x00")) {
		return Report{}, errors.New("this backup is not a supported SQLite database")
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return Report{}, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		return Report{}, err
	}
	// A complete Nx snapshot can retain WAL format markers. Deserialization
	// needs rollback format because an in-memory snapshot has no WAL sidecar.
	input := data
	if data[18] == 2 && data[19] == 2 {
		input = bytes.Clone(data)
		defer clear(input)
		input[18], input[19] = 1, 1
	}
	err = conn.Raw(func(driver any) error {
		d, ok := driver.(interface{ Deserialize([]byte) error })
		if !ok {
			return errors.New("SQLite memory loading is unavailable")
		}
		return d.Deserialize(input)
	})
	conn.Close()
	if err != nil {
		return Report{}, errors.New("could not load the SQLite backup into memory")
	}
	if _, err = db.ExecContext(ctx, "PRAGMA temp_store=MEMORY; PRAGMA trusted_schema=OFF; PRAGMA query_only=ON;"); err != nil {
		return Report{}, err
	}
	var check string
	if err = db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		if ctx.Err() != nil {
			return Report{}, ctx.Err()
		}
		return Report{}, errors.New("the downloaded database failed its SQLite integrity check")
	}
	report, err := readReportDatabase(ctx, Options{ShowPasswords: true, IncludeVirtual: true}, db)
	if err != nil {
		return Report{}, fmt.Errorf("unsupported or invalid Nx database: %w", err)
	}
	return report, nil
}
