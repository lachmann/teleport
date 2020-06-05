package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var (
	widgetsTableDefinition = readTableFromConfigFile("test/example_widgets.yaml")
	fullStrategyOpts       = map[string]string{}
)

func TestLoadNewTable(t *testing.T) {
	runDatabaseTest(t, func(t *testing.T, srcdb *sql.DB, destdb *sql.DB) {
		srcdb.Exec(widgetsTableDefinition.generateCreateTableStatement("widgets"))
		importCSV("testsrc", "widgets", "test/example_widgets.csv", widgetsTableDefinition.Columns)

		redirectLogs(t, func() {
			extractLoadDatabase("testsrc", "testdest", "widgets", "full", fullStrategyOpts)

			assertRowCount(t, 3, destdb, "testsrc_widgets")
		})
	})
}

func TestLoadSourceHasAdditionalColumn(t *testing.T) {
	runDatabaseTest(t, func(t *testing.T, srcdb *sql.DB, destdb *sql.DB) {
		// Create a new Table Definition, same as widgets, but without the `description` column
		widgetsWithoutDescription := Table{"example", "widgets", make([]Column, 0)}
		widgetsWithoutDescription.Columns = append(widgetsWithoutDescription.Columns, widgetsTableDefinition.Columns[:2]...)
		widgetsWithoutDescription.Columns = append(widgetsWithoutDescription.Columns, widgetsTableDefinition.Columns[3:]...)

		srcdb.Exec(widgetsTableDefinition.generateCreateTableStatement("widgets"))
		destdb.Exec(widgetsWithoutDescription.generateCreateTableStatement("testsrc_widgets"))
		importCSV("testsrc", "widgets", "test/example_widgets.csv", widgetsTableDefinition.Columns)

		expectLogMessage(t, "source table column `description` excluded", func() {
			extractLoadDatabase("testsrc", "testdest", "widgets", "full", fullStrategyOpts)

			assertRowCount(t, 3, destdb, "testsrc_widgets")
		})
	})
}

func TestLoadStringNotLongEnough(t *testing.T) {
	runDatabaseTest(t, func(t *testing.T, srcdb *sql.DB, destdb *sql.DB) {
		// Create a new Table Definition, same as widgets, but with name LENGTH changed to 32
		widgetsWithShortName := Table{"example", "widgets", make([]Column, len(widgetsTableDefinition.Columns))}
		copy(widgetsWithShortName.Columns, widgetsTableDefinition.Columns)
		widgetsWithShortName.Columns[1] = Column{"name", STRING, map[Option]int{LENGTH: 32}}

		srcdb.Exec(widgetsTableDefinition.generateCreateTableStatement("widgets"))
		destdb.Exec(widgetsWithShortName.generateCreateTableStatement("testsrc_widgets"))

		expectLogMessage(t, "For string column `name`, destination LENGTH is too short", func() {
			extractLoadDatabase("testsrc", "testdest", "widgets", "full", fullStrategyOpts)
		})
	})
}

func TestIncrementalStrategy(t *testing.T) {
	runDatabaseTest(t, func(t *testing.T, srcdb *sql.DB, destdb *sql.DB) {
		objects := Table{"example", "objects", make([]Column, 3)}
		objects.Columns[0] = Column{"id", INTEGER, map[Option]int{BYTES: 8}}
		objects.Columns[1] = Column{"name", STRING, map[Option]int{LENGTH: 255}}
		objects.Columns[2] = Column{"updated_at", TIMESTAMP, map[Option]int{}}

		srcdb.Exec(objects.generateCreateTableStatement("objects"))
		statement, _ := srcdb.Prepare("INSERT INTO objects (id, name, updated_at) VALUES (?, ?, ?)")
		statement.Exec(1, "book", time.Now().Add(-7*24*time.Hour))
		statement.Exec(2, "tv", time.Now().Add(-1*24*time.Hour))
		statement.Exec(3, "chair", time.Now())
		statement.Close()

		redirectLogs(t, func() {
			strategyOpts := make(map[string]string)
			strategyOpts["primary_key"] = "id"
			strategyOpts["modified_at_column"] = "updated_at"
			strategyOpts["hours_ago"] = "36"
			extractLoadDatabase("testsrc", "testdest", "objects", "incremental", strategyOpts)

			assertRowCount(t, 2, destdb, "testsrc_objects")
		})
	})
}

func TestExportTimestamp(t *testing.T) {
	runDatabaseTest(t, func(t *testing.T, db *sql.DB, _ *sql.DB) {
		columns := make([]Column, 0)
		columns = append(columns, Column{"created_at", TIMESTAMP, map[Option]int{}})
		table := Table{"test1", "timestamps", columns}

		db.Exec(table.generateCreateTableStatement("timestamps"))
		db.Exec("INSERT INTO timestamps (created_at) VALUES (DATETIME(1092941466, 'unixepoch'))")
		db.Exec("INSERT INTO timestamps (created_at) VALUES (NULL)")

		redirectLogs(t, func() {
			tempfile, _ := exportCSV("testsrc", "timestamps", columns, "")

			assertCsvCellContents(t, "2004-08-19 18:51:06", tempfile, 0, 0)
			assertCsvCellContents(t, "", tempfile, 1, 0)
		})
	})
}

func runDatabaseTest(t *testing.T, testfn func(*testing.T, *sql.DB, *sql.DB)) {
	Connections["testsrc"] = Connection{"testsrc", Configuration{"sqlite://:memory:", map[string]string{}}}
	dbSrc, err := connectDatabase("testsrc")
	if err != nil {
		assert.FailNow(t, "%w", err)
	}
	defer delete(dbs, "testsrc")

	Connections["testdest"] = Connection{"testdest", Configuration{"sqlite://:memory:", map[string]string{}}}
	dbDest, err := connectDatabase("testdest")
	if err != nil {
		assert.FailNow(t, "%w", err)
	}
	defer delete(dbs, "testdest")

	testfn(t, dbSrc, dbDest)
}

func assertRowCount(t *testing.T, expected int, database *sql.DB, table string) {
	row := database.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table))
	var count int
	err := row.Scan(&count)
	if err != nil {
		assert.FailNow(t, "%w", err)
	}
	assert.Equal(t, expected, count, "the number of rows is different than expected")
}

func assertCsvCellContents(t *testing.T, expected string, csvfilename string, row int, col int) {
	csvfile, err := os.Open(csvfilename)
	if err != nil {
		assert.FailNow(t, "%w", err)
	}

	reader := csv.NewReader(bufio.NewReader(csvfile))

	rowItr := 0

	for {
		line, err := reader.Read()
		if err == io.EOF {
			assert.FailNow(t, "fewer than %d rows in CSV", row)
		} else if err != nil {
			assert.FailNow(t, "%w", err)
		}

		if row != rowItr {
			rowItr++
			break
		}

		assert.EqualValues(t, expected, line[col])
		return
	}
}

func expectLogMessage(t *testing.T, message string, fn func()) {
	logBuffer := redirectLogs(t, fn)

	assert.Contains(t, logBuffer.String(), message)
}

func redirectLogs(t *testing.T, fn func()) (buffer bytes.Buffer) {
	log.SetOutput(&buffer)
	defer log.SetOutput(os.Stdout)

	fn()

	t.Log(buffer.String())
	return
}