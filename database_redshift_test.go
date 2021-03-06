// +build redshift

package main

import (
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/hundredwatt/teleport/schema"
	"github.com/stretchr/testify/assert"
)

func TestRedshiftCreateTableText(t *testing.T) {
	fromPostgresToRedshift(t, func(t *testing.T, _ *sql.DB, destdb *sql.DB) {
		objects := schema.Table{"", "objects", make([]schema.Column, 1)}
		objects.Columns[0] = schema.Column{"description", schema.TEXT, map[schema.Option]int{}}

		createTable("testdest", "objects", &objects)
		defer destdb.Exec(`DROP TABLE IF EXISTS objects`)

		table, _ := schema.DumpTableMetadata(destdb, "objects")

		assert.Equal(t, schema.Column{"description", schema.STRING, map[schema.Option]int{schema.LENGTH: 65535}}, table.Columns[0])
	})
}

func TestRedshiftLoadTest(t *testing.T) {
	fromPostgresToRedshift(t, func(t *testing.T, srcdb *sql.DB, destdb *sql.DB) {
		srcdb.Exec(widgetsTableDefinition.GenerateCreateTableStatement("widgets"))

		redirectLogs(t, func() {
			err := importCSV("testsrc", "widgets", "test/example_widgets.csv", widgetsTableDefinition.Columns)
			assert.NoError(t, err)
			extractLoadDatabase("testsrc", "testdest", "widgets", fullStrategyOpts)

			newTable, err := schema.DumpTableMetadata(destdb, "testsrc_widgets")
			assert.NoError(t, err)
			assertRowCount(t, 10, destdb, "testsrc_widgets")

			var (
				created_at  time.Time
				launched    string
				description string
			)
			row := destdb.QueryRow(`SELECT created_at, launched, description FROM testsrc_widgets WHERE ID = 1`)
			err = row.Scan(&created_at, &launched, &description)
			assert.NoError(t, err)
			createdAt, err := time.Parse(time.RFC3339, "2020-03-11T23:28:21-06:00")
			assert.NoError(t, err)
			assert.Equal(t, createdAt.UTC(), created_at.UTC())
			assert.Contains(t, launched, "2015-11-06")
			assert.Contains(t, description, "* Officiis. \n* Sapiente.")

			expectedColumns := widgetsTableDefinition.Columns
			expectedColumns[7] = schema.Column{"description", schema.STRING, map[schema.Option]int{schema.LENGTH: 65535}}
			assert.Equal(t, expectedColumns, newTable.Columns)
		})
	})
}

func fromPostgresToRedshift(t *testing.T, testfn func(*testing.T, *sql.DB, *sql.DB)) {
	redshiftURL := os.ExpandEnv("redshift://$TEST_REDSHIFT_USER:$TEST_REDSHIFT_PASSWORD@$TEST_REDSHIFT_HOST:5439/dev")
	options := map[string]string{"s3_bucket": "teleportdata", "s3_bucket_region": "us-east-1", "service_role": "arn:aws:iam::102693510702:role/RedshiftRole"}

	Databases["testsrc"] = Database{"postgres://postgres@localhost:45432/?sslmode=disable", map[string]string{}, true}
	srcdb, err := connectDatabase("testsrc")
	if err != nil {
		assert.FailNow(t, "%w", err)
	}
	defer delete(dbs, "testsrc")

	Databases["testdest"] = Database{redshiftURL, options, false}
	destdb, err := connectDatabase("testdest")
	if err != nil {
		assert.FailNow(t, "%w", err)
	}
	defer delete(dbs, "testdest")

	defer srcdb.Exec(`DROP TABLE IF EXISTS widgets`)
	defer destdb.Exec(`DROP TABLE IF EXISTS testsrc_widgets`)

	testfn(t, srcdb, destdb)
}
