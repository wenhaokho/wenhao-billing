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

// RowDiff is one row difference. A row present on both sides but different
// carries both texts; a row present on one side only carries that side's text
// alone. Key is the row's Go primary key (or, for journal lines, the Go entry
// id the lines belong to), or its Python key when no Go row pairs with it. The
// texts are the canonical column=value form, so the report never carries a DSN
// or a connection error.
type RowDiff struct {
	Table  string `json:"table"`
	Key    string `json:"key,omitempty"`
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

// uuidText matches a canonical uuid inside a text value, such as the payment
// and invoice ids in a journal entry's memo.
var uuidText = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// IDMaps is the Python → Go id map, keyed by entity (table). A table's map is
// applied only to that table's primary key and to the foreign-key columns that
// reference that table, so an integer account id never rewrites an unrelated
// integer column that happens to hold the same number.
type IDMaps map[string]map[string]string

// Set records that the Python id py of table is the Go id goID. Mapping one
// Python id to two different Go ids is an error: the run cannot tell which
// row is which.
func (m IDMaps) Set(table, py, goID string) error {
	t := m[table]
	if t == nil {
		t = map[string]string{}
		m[table] = t
	}
	if prev, ok := t[py]; ok && prev != goID {
		return fmt.Errorf("%s id %s maps to two Go ids (%s and %s)", table, py, prev, goID)
	}
	t[py] = goID
	return nil
}

// lookup is table's Go id for the Python id py.
func (m IDMaps) lookup(table, py string) (string, bool) {
	goID, ok := m[table][py]
	return goID, ok
}

// uuidMap is every mapped id that is a uuid, across all tables. A uuid is
// unique across tables, so a uuid inside a text value (a memo, a log reason,
// a JSON column) is replaced by its Go id whatever table it belongs to.
func (m IDMaps) uuidMap() map[string]string {
	out := map[string]string{}
	for _, t := range m {
		for py, goID := range t {
			if uuidText.MatchString(py) && len(py) == 36 {
				out[strings.ToLower(py)] = goID
			}
		}
	}
	return out
}

// Schema is the database's primary and foreign keys, read from pg_constraint
// at run time so the row comparison never relies on a hard-coded list.
type Schema struct {
	// PK is each table's single-column primary key.
	PK map[string]string
	// FK is, per table, each single-column foreign key and the table it
	// references.
	FK map[string]map[string]string
}

// LoadSchema reads the public schema's single-column primary and foreign keys.
func LoadSchema(ctx context.Context, pool *pgxpool.Pool) (Schema, error) {
	rows, err := pool.Query(ctx, `
SELECT cl.relname, c.contype::text, a.attname, COALESCE(ref.relname, '')
FROM pg_constraint c
JOIN pg_class cl ON cl.oid = c.conrelid
JOIN pg_namespace n ON n.oid = cl.relnamespace
JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
LEFT JOIN pg_class ref ON ref.oid = c.confrelid
WHERE n.nspname = 'public' AND c.contype IN ('p', 'f') AND cardinality(c.conkey) = 1`)
	if err != nil {
		return Schema{}, err
	}
	defer rows.Close()
	s := Schema{PK: map[string]string{}, FK: map[string]map[string]string{}}
	for rows.Next() {
		var table, kind, column, ref string
		if err := rows.Scan(&table, &kind, &column, &ref); err != nil {
			return Schema{}, err
		}
		if kind == "p" {
			s.PK[table] = column
			continue
		}
		if s.FK[table] == nil {
			s.FK[table] = map[string]string{}
		}
		s.FK[table][column] = ref
	}
	return s, rows.Err()
}

// refTable is the entity a column's value names: the table itself for its
// primary key, the referenced table for a foreign key, and the named
// polymorphic rule for journal_entries.source_id.
func (s Schema) refTable(table, column string, row Row) (string, bool) {
	if s.PK[table] == column {
		return table, true
	}
	if ref, ok := s.FK[table][column]; ok {
		return ref, true
	}
	if table == "journal_entries" && column == "source_id" {
		ref, ok := journalSourceTables[valueString(row["source_type"])]
		return ref, ok
	}
	return "", false
}

// journalSourceTables is the polymorphic journal_entries.source_id rule: the
// source_type names the table the source_id belongs to. Python's ledger posts
// PAYMENT entries only (app/services/ledger.py), and a reversal copies the
// original's source_type and source_id.
var journalSourceTables = map[string]string{
	"PAYMENT": "payments",
}

// EntityForKey is the table a JSON key or column name names, when the name
// is unambiguous: the primary-key column of exactly one table, or a foreign
// key that always references the same table. "id" (business_profile and
// payment_account) and "line_item_id" (three line-item tables) are ambiguous
// and name no table.
func (s Schema) EntityForKey(name string) (string, bool) {
	tables := map[string]bool{}
	for table, pk := range s.PK {
		if pk == name {
			tables[table] = true
		}
	}
	for _, fks := range s.FK {
		if ref, ok := fks[name]; ok {
			tables[ref] = true
		}
	}
	if len(tables) != 1 {
		return "", false
	}
	for table := range tables {
		return table, true
	}
	return "", false
}

// Snapshot is each touched table's rows before the run, keyed by primary key,
// as raw row text. The comparison then reads only rows the run created,
// changed or deleted, so a seeded row the scenarios never touched is never
// reported, even when the two databases differ there.
type Snapshot map[string]map[string]string

// TakeSnapshot reads every touched table's rows on one database.
func TakeSnapshot(ctx context.Context, pool *pgxpool.Pool, schema Schema, tables []string) (Snapshot, error) {
	snap := Snapshot{}
	for _, table := range tables {
		if err := checkTable(schema, table); err != nil {
			return nil, err
		}
		if goOnlyTables[table] {
			continue
		}
		rows, err := queryRows(ctx, pool, table)
		if err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", table, err)
		}
		pk := schema.PK[table]
		byKey := make(map[string]string, len(rows))
		for _, row := range rows {
			byKey[valueString(row[pk])] = rawRow(row)
		}
		snap[table] = byKey
	}
	return snap, nil
}

// checkTable refuses a table name that is not a plain identifier, or a table
// with no single-column primary key: such a table cannot be snapshotted.
func checkTable(schema Schema, table string) error {
	if !tableName.MatchString(table) {
		return fmt.Errorf("row comparison: table %q is not a valid table name", table)
	}
	if goOnlyTables[table] {
		return nil
	}
	if schema.PK[table] == "" {
		return fmt.Errorf("row comparison: table %q has no single-column primary key", table)
	}
	return nil
}

// RowsInput is one row comparison's inputs. The snapshots are the two
// databases before the run; Maps is the ids the responses named, and the
// comparison adds the pairs it derives.
type RowsInput struct {
	Python, Go     *pgxpool.Pool
	Schema         Schema
	Tables         []string
	Maps           IDMaps
	PythonSnapshot Snapshot
	GoSnapshot     Snapshot
}

// tableState is one table's touched rows on both sides: the pre-existing rows
// the run changed or deleted on either side (keyed by their shared primary
// key, since both databases are restored from one dump), and the rows the run
// created on each side.
type tableState struct {
	table  string
	pk     string
	prePKs []string
	pyPre  map[string]Row
	goPre  map[string]Row
	pyNew  []Row
	goNew  []Row
}

// Rows compares the rows the run touched. It reads each table on both
// databases, keeps the rows created, changed or deleted since the snapshots,
// derives the Python → Go pairs no response named (in derivationRules order),
// and then compares:
//   - a pre-existing row by its shared primary key;
//   - a created row with the Go row its mapped id names;
//   - journal lines as a multiset per mapped entry, line_id excluded.
//
// A row that pairs with nothing is reported as Python-only or Go-only; it is
// never dropped.
func Rows(ctx context.Context, in RowsInput) ([]RowDiff, error) {
	states := map[string]*tableState{}
	var order []string
	for _, table := range in.Tables {
		if err := checkTable(in.Schema, table); err != nil {
			return nil, err
		}
		if goOnlyTables[table] || states[table] != nil {
			continue
		}
		st, err := readState(ctx, in, table)
		if err != nil {
			return nil, err
		}
		states[table] = st
		order = append(order, table)
	}

	c := rowComparer{schema: in.Schema, maps: in.Maps}
	for _, rule := range derivationRules {
		if st := states[rule.table]; st != nil {
			if err := c.derive(rule, st); err != nil {
				return nil, err
			}
		}
	}
	c.uuids = in.Maps.uuidMap()

	var diffs []RowDiff
	for _, table := range order {
		st := states[table]
		if group, ok := multisetTables[table]; ok {
			diffs = append(diffs, c.compareMultiset(st, group)...)
			continue
		}
		diffs = append(diffs, c.comparePaired(st)...)
	}
	return diffs, nil
}

// readState reads one table on both sides and keeps its touched rows.
func readState(ctx context.Context, in RowsInput, table string) (*tableState, error) {
	pyRows, err := queryRows(ctx, in.Python, table)
	if err != nil {
		return nil, fmt.Errorf("row comparison: python %s: %w", table, err)
	}
	goRows, err := queryRows(ctx, in.Go, table)
	if err != nil {
		return nil, fmt.Errorf("row comparison: go %s: %w", table, err)
	}
	pk := in.Schema.PK[table]
	st := &tableState{table: table, pk: pk, pyPre: map[string]Row{}, goPre: map[string]Row{}}
	pySnap, goSnap := in.PythonSnapshot[table], in.GoSnapshot[table]
	touched := map[string]bool{}
	split := func(rows []Row, snap map[string]string) (pre map[string]Row, created []Row) {
		pre = map[string]Row{}
		for _, row := range rows {
			key := valueString(row[pk])
			before, existed := snap[key]
			if !existed {
				created = append(created, row)
				continue
			}
			pre[key] = row
			if before != rawRow(row) {
				touched[key] = true
			}
		}
		for key := range snap {
			if _, ok := pre[key]; !ok {
				touched[key] = true // deleted by the run
			}
		}
		return pre, created
	}
	pyAll, pyNew := split(pyRows, pySnap)
	goAll, goNew := split(goRows, goSnap)
	st.pyNew, st.goNew = pyNew, goNew
	for key := range touched {
		st.prePKs = append(st.prePKs, key)
		if row, ok := pyAll[key]; ok {
			st.pyPre[key] = row
		}
		if row, ok := goAll[key]; ok {
			st.goPre[key] = row
		}
	}
	sort.Strings(st.prePKs)
	return st, nil
}

// derivationRule pairs created rows whose ids no response named. key returns
// the columns the pair must agree on (after the Python side's ids are mapped);
// rows that share a key pair in orderBy order.
//
// via, when set, is a self-referencing column: the rows where it is null
// pair first, then a row where it is set pairs with the row whose via points
// at its own via's partner, and only the rows left pair by key and orderBy.
type derivationRule struct {
	table   string
	key     func(Row) []string
	orderBy string
	via     string
}

// cols is a fixed key.
func cols(c ...string) func(Row) []string { return func(Row) []string { return c } }

// derivationRules is the fixed order the missing pairs are derived in; a
// later rule's key may use an earlier rule's pairs. The column names are the
// real ones (migrations/00001_baseline.sql, backend-python/app/models):
//   - payments: the mapped invoice_id and payer_reference, or amount and
//     currency when payer_reference is empty;
//   - journal_entries: source_type, the mapped source_id and the memo (whose
//     uuids are mapped), in posted_at order; a reversed entry pairs through
//     its reversal (reversed_by_entry_id) first, since Go dates a payment
//     reversal at its original's posted_at and two clearings of one payment
//     then tie on posted_at (PORT-NOTES 167);
//   - reconciliation_log: the mapped payment_id and action, in created_at order;
//   - invoice, quotation and bill line items: the mapped parent and position;
//   - payment_account: business_profile_id and currency (PUT replaces them all);
//   - business_profile: its singleton id.
//
// There is no bill_payments table: a bill payment only updates the bill row.
var derivationRules = []derivationRule{
	{table: "payments", key: func(r Row) []string {
		if valueString(r["payer_reference"]) != "" {
			return []string{"invoice_id", "payer_reference"}
		}
		return []string{"invoice_id", "amount", "currency"}
	}, orderBy: "created_at"},
	{table: "journal_entries", key: cols("source_type", "source_id", "memo"), orderBy: "posted_at", via: "reversed_by_entry_id"},
	{table: "reconciliation_log", key: cols("payment_id", "action"), orderBy: "created_at"},
	{table: "invoice_line_items", key: cols("invoice_id", "position"), orderBy: "created_at"},
	{table: "quotation_line_items", key: cols("quotation_id", "position"), orderBy: "created_at"},
	{table: "bill_line_items", key: cols("bill_id", "position"), orderBy: "created_at"},
	{table: "payment_account", key: cols("business_profile_id", "currency"), orderBy: "updated_at"},
	{table: "business_profile", key: cols("id"), orderBy: "updated_at"},
}

// multisetTables compare as a multiset per group: journal lines are grouped by
// their mapped entry and compared with line_id excluded, so a line moved to
// another entry with the same totals is still reported.
var multisetTables = map[string]string{
	"journal_lines": "entry_id",
}

// rowComparer applies the id maps and the normalisation rules.
type rowComparer struct {
	schema Schema
	maps   IDMaps
	uuids  map[string]string
}

// newRowComparer is a comparer over the final maps, for the step answers.
func newRowComparer(schema Schema, maps IDMaps) *rowComparer {
	return &rowComparer{schema: schema, maps: maps, uuids: maps.uuidMap()}
}

// normalizeBody applies the row rules to a JSON answer: a server-clock
// timestamp key becomes <timestamp> (null stays null), a generated number
// becomes <generated>, and on the Python side an id under a key that names
// one table is mapped through that table's map, and any other mapped uuid in
// a string is replaced. A body that is not JSON is returned as it is.
func (c *rowComparer) normalizeBody(body []byte, python bool) []byte {
	if len(strings.TrimSpace(string(body))) == 0 {
		return body
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return body
	}
	out, err := json.Marshal(c.normalizeJSON("", v, python))
	if err != nil {
		return body
	}
	return out
}

// normalizeJSON is normalizeBody's walk; key is the object key the value sits
// under (an array's elements inherit it).
func (c *rowComparer) normalizeJSON(key string, v any, python bool) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			x[k] = c.normalizeJSON(k, val, python)
		}
		return x
	case []any:
		for i := range x {
			x[i] = c.normalizeJSON(key, x[i], python)
		}
		return x
	case nil, bool:
		return v
	}
	s := fmt.Sprint(v)
	if timestampColumns[key] {
		return "<timestamp>"
	}
	for column, pattern := range generatedColumns {
		if strings.HasSuffix(column, "."+key) && pattern.MatchString(s) {
			return "<generated>"
		}
	}
	if !python {
		return v
	}
	if table, ok := c.schema.EntityForKey(key); ok {
		if goID, ok := c.maps.lookup(table, s); ok {
			if _, isNumber := v.(json.Number); isNumber {
				return json.Number(goID)
			}
			return goID
		}
	}
	if str, ok := v.(string); ok {
		return replaceUUIDs(str, c.uuids)
	}
	return v
}

// derive pairs one table's unpaired created rows by the rule's key. A row
// already in the map (a response named it) is left as it is, and so is a Go
// row some Python id already maps to.
func (c *rowComparer) derive(rule derivationRule, st *tableState) error {
	if rule.via == "" {
		return c.deriveWhere(rule, st, nil)
	}
	unset := func(r Row) bool { return valueString(r[rule.via]) == "" }
	if err := c.deriveWhere(rule, st, unset); err != nil {
		return err
	}
	if err := c.deriveVia(rule, st); err != nil {
		return err
	}
	return c.deriveWhere(rule, st, nil)
}

// deriveVia pairs each unpaired Python row whose via is set with the
// unpaired Go row whose via is its via's Go partner.
func (c *rowComparer) deriveVia(rule derivationRule, st *tableState) error {
	taken := c.takenGo(st)
	goByVia := map[string]Row{}
	for _, row := range st.goNew {
		if v := valueString(row[rule.via]); v != "" && !taken[valueString(row[st.pk])] {
			goByVia[v] = row
		}
	}
	pys := append([]Row(nil), st.pyNew...)
	sortRows(pys, rule.orderBy, st.pk)
	for _, row := range pys {
		v := valueString(row[rule.via])
		if v == "" {
			continue
		}
		if _, ok := c.maps.lookup(st.table, valueString(row[st.pk])); ok {
			continue
		}
		goVia, ok := c.maps.lookup(st.table, v)
		if !ok {
			continue
		}
		gov, ok := goByVia[goVia]
		if !ok {
			continue
		}
		if err := c.maps.Set(st.table, valueString(row[st.pk]), valueString(gov[st.pk])); err != nil {
			return err
		}
		delete(goByVia, goVia)
	}
	return nil
}

// takenGo is the Go ids of st's table that some Python id already maps to.
func (c *rowComparer) takenGo(st *tableState) map[string]bool {
	taken := map[string]bool{}
	for _, goID := range c.maps[st.table] {
		taken[goID] = true
	}
	return taken
}

// deriveWhere pairs the unpaired rows that match (all when match is nil) by
// the rule's key, in orderBy order.
func (c *rowComparer) deriveWhere(rule derivationRule, st *tableState, match func(Row) bool) error {
	c.uuids = c.maps.uuidMap()
	taken := c.takenGo(st)
	pyGroups := map[string][]Row{}
	for _, row := range st.pyNew {
		if match != nil && !match(row) {
			continue
		}
		if _, ok := c.maps.lookup(st.table, valueString(row[st.pk])); ok {
			continue
		}
		k := c.keyOf(st.table, row, rule.key(row), true)
		pyGroups[k] = append(pyGroups[k], row)
	}
	goGroups := map[string][]Row{}
	for _, row := range st.goNew {
		if match != nil && !match(row) {
			continue
		}
		if taken[valueString(row[st.pk])] {
			continue
		}
		k := c.keyOf(st.table, row, rule.key(row), false)
		goGroups[k] = append(goGroups[k], row)
	}
	keys := make([]string, 0, len(pyGroups))
	for k := range pyGroups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		pys, gos := pyGroups[k], goGroups[k]
		sortRows(pys, rule.orderBy, st.pk)
		sortRows(gos, rule.orderBy, st.pk)
		for i := 0; i < len(pys) && i < len(gos); i++ {
			if err := c.maps.Set(st.table, valueString(pys[i][st.pk]), valueString(gos[i][st.pk])); err != nil {
				return err
			}
		}
	}
	return nil
}

// keyOf is a row's pairing key: the named columns, the Python side's ids
// mapped.
func (c *rowComparer) keyOf(table string, row Row, columns []string, python bool) string {
	parts := make([]string, len(columns))
	for i, column := range columns {
		parts[i] = column + "=" + c.mapped(table, column, row, python)
	}
	return strings.Join(parts, "\x1f")
}

// sortRows orders rows by a column (a timestamp orders by time), then by
// primary key, so a tie pairs in a fixed order.
func sortRows(rows []Row, column, pk string) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := orderValue(rows[i][column]), orderValue(rows[j][column])
		if a != b {
			return a < b
		}
		return valueString(rows[i][pk]) < valueString(rows[j][pk])
	})
}

// orderValue is a value's sort text; a time has a fixed width so it sorts
// as text.
func orderValue(v any) string {
	if t, ok := v.(time.Time); ok {
		return t.UTC().Format("2006-01-02T15:04:05.000000000")
	}
	return valueString(v)
}

// comparePaired compares a table whose rows pair by primary key.
func (c *rowComparer) comparePaired(st *tableState) []RowDiff {
	var diffs []RowDiff
	for _, key := range st.prePKs {
		py, pyOK := st.pyPre[key]
		gov, goOK := st.goPre[key]
		if !pyOK && !goOK {
			continue // deleted on both sides: the backends agree
		}
		diffs = append(diffs, c.pair(st.table, key, py, pyOK, gov, goOK, nil)...)
	}
	goByPK := map[string]Row{}
	for _, row := range st.goNew {
		goByPK[valueString(row[st.pk])] = row
	}
	used := map[string]bool{}
	for _, py := range st.pyNew {
		pyKey := valueString(py[st.pk])
		goID, ok := c.maps.lookup(st.table, pyKey)
		gov, found := goByPK[goID]
		if !ok || !found || used[goID] {
			diffs = append(diffs, RowDiff{Table: st.table, Key: pyKey, Python: c.canonical(st.table, py, true, nil)})
			continue
		}
		used[goID] = true
		diffs = append(diffs, c.pair(st.table, goID, py, true, gov, true, nil)...)
	}
	for _, gov := range st.goNew {
		goKey := valueString(gov[st.pk])
		if !used[goKey] {
			diffs = append(diffs, RowDiff{Table: st.table, Key: goKey, Go: c.canonical(st.table, gov, false, nil)})
		}
	}
	return diffs
}

// pair compares one row pair; a side that is missing is reported alone.
func (c *rowComparer) pair(table, key string, py Row, pyOK bool, gov Row, goOK bool, exclude map[string]bool) []RowDiff {
	var p, g string
	if pyOK {
		p = c.canonical(table, py, true, exclude)
	}
	if goOK {
		g = c.canonical(table, gov, false, exclude)
	}
	if pyOK && goOK && p == g {
		return nil
	}
	return []RowDiff{{Table: table, Key: key, Python: p, Go: g}}
}

// compareMultiset compares a table's touched rows as a multiset per mapped
// group, the primary key excluded.
func (c *rowComparer) compareMultiset(st *tableState, group string) []RowDiff {
	exclude := map[string]bool{st.pk: true}
	count := func(rows []Row, python bool) map[string]map[string]int {
		out := map[string]map[string]int{}
		for _, row := range rows {
			g := c.mapped(st.table, group, row, python)
			if out[g] == nil {
				out[g] = map[string]int{}
			}
			out[g][c.canonical(st.table, row, python, exclude)]++
		}
		return out
	}
	pyRows := append([]Row{}, st.pyNew...)
	goRows := append([]Row{}, st.goNew...)
	for _, key := range st.prePKs {
		if row, ok := st.pyPre[key]; ok {
			pyRows = append(pyRows, row)
		}
		if row, ok := st.goPre[key]; ok {
			goRows = append(goRows, row)
		}
	}
	pyCounts, goCounts := count(pyRows, true), count(goRows, false)
	groups := map[string]bool{}
	for g := range pyCounts {
		groups[g] = true
	}
	for g := range goCounts {
		groups[g] = true
	}
	var diffs []RowDiff
	for _, g := range sortedKeys(groups) {
		texts := map[string]bool{}
		for t := range pyCounts[g] {
			texts[t] = true
		}
		for t := range goCounts[g] {
			texts[t] = true
		}
		for _, t := range sortedKeys(texts) {
			p, q := pyCounts[g][t], goCounts[g][t]
			for i := 0; i < p-q; i++ {
				diffs = append(diffs, RowDiff{Table: st.table, Key: g, Python: t})
			}
			for i := 0; i < q-p; i++ {
				diffs = append(diffs, RowDiff{Table: st.table, Key: g, Go: t})
			}
		}
	}
	return diffs
}

// sortedKeys is a set's members in order.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// canonical is a row's columns in a fixed order, each value normalised, with
// the excluded columns left out.
func (c *rowComparer) canonical(table string, row Row, python bool, exclude map[string]bool) string {
	columns := make([]string, 0, len(row))
	for column := range row {
		if !exclude[column] {
			columns = append(columns, column)
		}
	}
	sort.Strings(columns)
	var b strings.Builder
	for _, column := range columns {
		if b.Len() > 0 {
			b.WriteByte(';')
		}
		b.WriteString(column)
		b.WriteByte('=')
		b.WriteString(c.normalized(table, column, row, python))
	}
	return b.String()
}

// normalized applies the named rules to one column: a server-clock timestamp
// becomes <timestamp> (a NULL stays empty), a server-generated number becomes
// <generated>, and on the Python side an id is mapped to its Go id.
func (c *rowComparer) normalized(table, column string, row Row, python bool) string {
	s := c.mapped(table, column, row, python)
	if timestampColumns[column] && s != "" {
		return "<timestamp>"
	}
	if pattern, ok := generatedColumns[table+"."+column]; ok && pattern.MatchString(s) {
		return "<generated>"
	}
	return s
}

// mapped is one column's text with the Python side's ids mapped: a key column
// through its own table's map, any other text through the uuid map.
func (c *rowComparer) mapped(table, column string, row Row, python bool) string {
	s := valueString(row[column])
	if !python || s == "" {
		return s
	}
	if ref, ok := c.schema.refTable(table, column, row); ok {
		if goID, ok := c.maps.lookup(ref, s); ok {
			return goID
		}
		return s
	}
	return replaceUUIDs(s, c.uuids)
}

// replaceUUIDs replaces every mapped uuid inside a text value.
func replaceUUIDs(s string, uuids map[string]string) string {
	if len(uuids) == 0 {
		return s
	}
	return uuidText.ReplaceAllStringFunc(s, func(u string) string {
		if goID, ok := uuids[strings.ToLower(u)]; ok {
			return goID
		}
		return u
	})
}

// timestampColumns are the server-clock columns: each is set from the
// backend's own clock when the row is written, so its value is never
// compared, only whether it is set.
var timestampColumns = map[string]bool{
	"created_at":   true,
	"updated_at":   true,
	"posted_at":    true,
	"accepted_at":  true,
	"last_sent_at": true,
}

// generatedColumns are the numbers the server assigns, with the shape it
// assigns them in (backend-python app/services/invoicing.py
// generate_invoice_number and app/services/quoting.py
// generate_quotation_number). A value of another shape was supplied by the
// request and is compared. bill_number, po_number, po_so_number and
// account_number are always user-supplied and always compared.
var generatedColumns = map[string]*regexp.Regexp{
	"invoices.invoice_number":     regexp.MustCompile(`^WH[0-9]{4}[0-9]{4,}$`),
	"quotations.quotation_number": regexp.MustCompile(`^Q[0-9]{4}[0-9]{4,}$`),
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

// rawRow is a row's exact text, nothing normalised: the snapshot compares it
// to tell a changed row from an untouched one.
func rawRow(row Row) string {
	columns := make([]string, 0, len(row))
	for column := range row {
		columns = append(columns, column)
	}
	sort.Strings(columns)
	var b strings.Builder
	for _, column := range columns {
		b.WriteString(column)
		b.WriteByte('=')
		b.WriteString(valueString(row[column]))
		b.WriteByte(';')
	}
	return b.String()
}

// valueString renders a database value as text. The row comparison is a
// text comparison, so a value's type is not part of the comparison. A uuid
// (pgx returns a uuid column as [16]byte) renders as its canonical lowercase
// form, so it matches the id a JSON response carries.
func valueString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	case [16]byte:
		return uuid.UUID(v).String()
	case pgtype.UUID:
		if !v.Valid {
			return ""
		}
		return uuid.UUID(v.Bytes).String()
	case uuid.UUID:
		return v.String()
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
	case map[string]any, []any:
		b, _ := json.Marshal(v)
		return string(b)
	default:
		return fmt.Sprint(v)
	}
}
