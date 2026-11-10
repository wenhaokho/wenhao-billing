package compare

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// Row is one database row, its column names as keys.
type Row = map[string]any

// RowDiff is one row present on one side only. The row is the canonical
// column=value text, so the report never carries a DSN or a connection error.
type RowDiff struct {
	Table  string `json:"table"`
	Python string `json:"python,omitempty"`
	Go     string `json:"go,omitempty"`
}

// tableName is the plain identifier a table name may be. It is a first line
// of defence against SQL injection: the table name comes from YAML.
var tableName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// goOnlyTables are the tables the Python backend never has. They are not
// compared: they hold Go's own bookkeeping and are not a Python contract.
var goOnlyTables = map[string]bool{
	"goose_db_version":         true,
	"login_attempts":           true,
	"scheduled_job_runs":       true,
	"oauth_spent_codes":        true,
	"mcp_spent_confirm_tokens": true,
}

// Rows compares the rows of every table in the two databases. A row is
// compared as a multiset: order is not significant. Python's id is replaced by
// the Go id from idMap before the row is compared, so a row's created id and
// every foreign key it carries compare as the same row. Timestamps and
// generated numbers are replaced by named placeholders, so a row's clock time
// and its generated number never cause a difference.
//
// A Go-only table is skipped. A table name that is not a plain identifier is
// refused before any query is sent.
func Rows(ctx context.Context, py, goDB *pgxpool.Pool, tables []string, idMap map[string]string) ([]RowDiff, error) {
	var diffs []RowDiff
	for _, table := range tables {
		if !tableName.MatchString(table) {
			return nil, fmt.Errorf("row comparison: table %q is not a valid table name", table)
		}
		if goOnlyTables[table] {
			continue
		}
		pyRows, err := queryRows(ctx, py, table)
		if err != nil {
			return nil, fmt.Errorf("row comparison: python %s: %w", table, err)
		}
		goRows, err := queryRows(ctx, goDB, table)
		if err != nil {
			return nil, fmt.Errorf("row comparison: go %s: %w", table, err)
		}
		diffs = append(diffs, compareRows(table, pyRows, goRows, idMap)...)
	}
	return diffs, nil
}

// queryRows reads every row of a table. The table name is validated by the
// caller and quoted here, so a name cannot change the query.
func queryRows(ctx context.Context, pool *pgxpool.Pool, table string) ([]Row, error) {
	rows, err := pool.Query(ctx, "SELECT * FROM "+pgx.Identifier{table}.Sanitize())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		row, err := pgx.RowToMap(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// compareRows compares two tables' rows as multisets. The Python row is
// normalised with the id map applied; the Go row is not, because its values
// already carry the Go ids.
func compareRows(table string, py, goRows []Row, idMap map[string]string) []RowDiff {
	pyCounts := make(map[string]int, len(py))
	for _, row := range py {
		pyCounts[canonicalRow(row, idMap, true)]++
	}
	goCounts := make(map[string]int, len(goRows))
	for _, row := range goRows {
		goCounts[canonicalRow(row, idMap, false)]++
	}
	keys := make([]string, 0, len(pyCounts)+len(goCounts))
	for key := range pyCounts {
		keys = append(keys, key)
	}
	for key := range goCounts {
		if _, ok := pyCounts[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	var diffs []RowDiff
	for _, key := range keys {
		p, g := pyCounts[key], goCounts[key]
		for i := 0; i < p-g; i++ {
			diffs = append(diffs, RowDiff{Table: table, Python: key})
		}
		for i := 0; i < g-p; i++ {
			diffs = append(diffs, RowDiff{Table: table, Go: key})
		}
	}
	return diffs
}

// canonicalRow is a row's columns in a fixed order, each value normalised. The
// id map is applied only on the Python side, so a Python id becomes the Go id.
func canonicalRow(row Row, idMap map[string]string, applyIDMap bool) string {
	columns := make([]string, 0, len(row))
	for column := range row {
		columns = append(columns, column)
	}
	sort.Strings(columns)
	var b strings.Builder
	for _, column := range columns {
		if b.Len() > 0 {
			b.WriteByte(';')
		}
		b.WriteString(column)
		b.WriteByte('=')
		b.WriteString(normalizeValue(column, row[column], idMap, applyIDMap))
	}
	return b.String()
}

// normalizeValue applies the named rules: a timestamp column becomes a fixed
// placeholder, a generated number column becomes a fixed placeholder, and a
// value that is an id in the map becomes the Go id on the Python side.
func normalizeValue(column string, value any, idMap map[string]string, applyIDMap bool) string {
	if isTimestampColumn(column) {
		return "<timestamp>"
	}
	if isGeneratedColumn(column) {
		return "<generated>"
	}
	s := valueString(value)
	if applyIDMap && isIDColumn(column) {
		if mapped, ok := idMap[s]; ok {
			s = mapped
		}
	}
	return s
}

// isIDColumn reports whether a column holds an id: the id column or a
// <name>_id column. A numeric column such as debit is never mapped, even when
// its text happens to be an id's text.
func isIDColumn(column string) bool {
	return column == "id" || strings.HasSuffix(column, "_id")
}

// isTimestampColumn reports whether a column is a timestamp the comparison
// normalises: created_at, updated_at and posted_at.
func isTimestampColumn(column string) bool {
	switch column {
	case "created_at", "updated_at", "posted_at":
		return true
	}
	return false
}

// isGeneratedColumn reports whether a column is a generated number: the
// invoice_number, quotation_number, bill_number, payment_number,
// receipt_number and journal_entry_number columns.
func isGeneratedColumn(column string) bool {
	return strings.HasSuffix(column, "_number")
}

// valueString renders a database value as text. The row comparison is a
// text comparison, so a value's type is not part of the comparison.
func valueString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	case time.Time:
		return v.UTC().Format(time.RFC3339Nano)
	case pgtype.Timestamptz:
		if !v.Valid {
			return ""
		}
		return v.Time.UTC().Format(time.RFC3339Nano)
	case pgtype.Timestamp:
		if !v.Valid {
			return ""
		}
		return v.Time.UTC().Format(time.RFC3339Nano)
	case pgtype.UUID:
		return v.String()
	case uuid.UUID:
		return v.String()
	case decimal.Decimal:
		return v.String()
	case int:
		return strconv.Itoa(v)
	case int8:
		return strconv.FormatInt(int64(v), 10)
	case int16:
		return strconv.FormatInt(int64(v), 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case bool:
		return strconv.FormatBool(v)
	case map[string]any:
		b, _ := json.Marshal(v)
		return string(b)
	case []any:
		b, _ := json.Marshal(v)
		return string(b)
	default:
		return fmt.Sprint(v)
	}
}
