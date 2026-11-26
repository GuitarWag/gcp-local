package gcplocaltest

import (
	"context"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/bigtable"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// bigtableClients dials the emulator using BIGTABLE_EMULATOR_HOST (the same
// path real users take) and returns a data + admin client pair.
func bigtableClients(t *testing.T) (*bigtable.Client, *bigtable.AdminClient) {
	t.Helper()
	em := testutil.Start(t)
	t.Setenv("BIGTABLE_EMULATOR_HOST", em.Host)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data, err := bigtable.NewClient(ctx, "testproj", "testinst")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = data.Close() })
	admin, err := bigtable.NewAdminClient(ctx, "testproj", "testinst")
	if err != nil {
		t.Fatalf("NewAdminClient: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	return data, admin
}

func mustTable(t *testing.T, admin *bigtable.AdminClient, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := admin.CreateTable(ctx, name); err != nil {
		if status.Code(err) == codes.AlreadyExists {
			return
		}
		t.Fatalf("CreateTable(%s): %v", name, err)
	}
	if err := admin.CreateColumnFamily(ctx, name, "cf"); err != nil {
		t.Fatalf("CreateColumnFamily: %v", err)
	}
}

func TestBigtableWriteReadRoundTripTimestamps(t *testing.T) {
	data, admin := bigtableClients(t)
	mustTable(t, admin, "rt")
	tbl := data.Open("rt")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Write with an explicit timestamp and one with server time.
	mut := bigtable.NewMutation()
	mut.Set("cf", "a", 1_000_000, []byte("v-a1"))
	mut.Set("cf", "b", bigtable.ServerTime, []byte("v-b1"))
	if err := tbl.Apply(ctx, "row1", mut); err != nil {
		t.Fatalf("Apply row1: %v", err)
	}
	mut2 := bigtable.NewMutation()
	mut2.Set("cf", "a", 2_000_000, []byte("v-a2"))
	if err := tbl.Apply(ctx, "row2", mut2); err != nil {
		t.Fatalf("Apply row2: %v", err)
	}

	row, err := tbl.ReadRow(ctx, "row1")
	if err != nil {
		t.Fatalf("ReadRow row1: %v", err)
	}
	if got := string(row["cf"][0].Value); got != "v-a1" {
		t.Errorf("row1 cf:a value = %q, want %q", got, "v-a1")
	}
	if got := row["cf"][0].Timestamp; got != 1_000_000 {
		t.Errorf("row1 cf:a timestamp = %d, want 1000000", got)
	}
	// Server-time cell must carry a positive millisecond-granular timestamp.
	if got := row["cf"][1].Timestamp; got <= 0 || got%1000 != 0 {
		t.Errorf("row1 cf:b server timestamp = %d, want positive ms multiple", got)
	}

	row2, err := tbl.ReadRow(ctx, "row2")
	if err != nil {
		t.Fatalf("ReadRow row2: %v", err)
	}
	if got := string(row2["cf"][0].Value); got != "v-a2" {
		t.Errorf("row2 cf:a value = %q, want v-a2", got)
	}
	if got := row2["cf"][0].Timestamp; got != 2_000_000 {
		t.Errorf("row2 cf:a timestamp = %d, want 2000000", got)
	}

	// Full scan sees both rows in key order.
	var keys []string
	err = tbl.ReadRows(ctx, bigtable.InfiniteRange(""), func(r bigtable.Row) bool {
		keys = append(keys, r.Key())
		return true
	})
	if err != nil {
		t.Fatalf("ReadRows scan: %v", err)
	}
	if len(keys) != 2 || keys[0] != "row1" || keys[1] != "row2" {
		t.Errorf("scan keys = %v, want [row1 row2]", keys)
	}
}

func TestBigtablePrefixAndChainFilterReads(t *testing.T) {
	data, admin := bigtableClients(t)
	mustTable(t, admin, "flt")
	tbl := data.Open("flt")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for _, rk := range []string{"pre-a", "pre-b", "other-c"} {
		m := bigtable.NewMutation()
		m.Set("cf", "col1", 1_000_000, []byte("val1-"+rk))
		m.Set("cf", "col2", 2_000_000, []byte("val2-"+rk))
		if err := tbl.Apply(ctx, rk, m); err != nil {
			t.Fatalf("Apply %s: %v", rk, err)
		}
	}

	// Prefix range returns only the prefixed rows.
	var got []string
	err := tbl.ReadRows(ctx, bigtable.PrefixRange("pre-"), func(r bigtable.Row) bool {
		got = append(got, r.Key())
		return true
	})
	if err != nil {
		t.Fatalf("prefix read: %v", err)
	}
	if len(got) != 2 || got[0] != "pre-a" || got[1] != "pre-b" {
		t.Errorf("prefix rows = %v, want [pre-a pre-b]", got)
	}

	// Chain filter: family + qualifier regex + strip value. Values must be
	// stripped but timestamps preserved; only col2 cells survive.
	chain := bigtable.ChainFilters(
		bigtable.FamilyFilter("cf"),
		bigtable.ColumnFilter("col2"),
		bigtable.StripValueFilter(),
	)
	err = tbl.ReadRows(ctx, bigtable.InfiniteRange(""), func(r bigtable.Row) bool {
		fam := r["cf"]
		if len(fam) != 1 {
			t.Errorf("row %s: got %d cells after chain filter, want 1", r.Key(), len(fam))
			return true
		}
		if fam[0].Column != "cf:col2" {
			t.Errorf("row %s: column = %s, want cf:col2", r.Key(), fam[0].Column)
		}
		if len(fam[0].Value) != 0 {
			t.Errorf("row %s: stripped value = %q, want empty", r.Key(), fam[0].Value)
		}
		if fam[0].Timestamp != 2_000_000 {
			t.Errorf("row %s: timestamp = %d, want 2000000", r.Key(), fam[0].Timestamp)
		}
		return true
	}, bigtable.RowFilter(chain))
	if err != nil {
		t.Fatalf("chain filter read: %v", err)
	}
}

func TestBigtableSampleRowKeys(t *testing.T) {
	data, admin := bigtableClients(t)
	mustTable(t, admin, "smp")
	tbl := data.Open("smp")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	written := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		m := bigtable.NewMutation()
		m.Set("cf", "q", bigtable.ServerTime, []byte{byte('v')})
		rk := "srow-" + strings.Repeat("x", i) + string(rune('a'+i))
		written = append(written, rk)
		if err := tbl.Apply(ctx, rk, m); err != nil {
			t.Fatalf("Apply %s: %v", rk, err)
		}
	}
	keys, err := tbl.SampleRowKeys(ctx)
	if err != nil {
		t.Fatalf("SampleRowKeys: %v", err)
	}
	if len(keys) == 0 {
		t.Fatal("SampleRowKeys returned no keys")
	}
	for i := 1; i < len(keys); i++ {
		if keys[i] <= keys[i-1] {
			t.Errorf("sample not strictly increasing at %d: %q <= %q", i, keys[i], keys[i-1])
		}
	}
	if keys[len(keys)-1] != written[len(written)-1] {
		t.Errorf("last sample key = %q, want final row key %q", keys[len(keys)-1], written[len(written)-1])
	}
}

func TestBigtableCheckAndMutateRow(t *testing.T) {
	data, admin := bigtableClients(t)
	mustTable(t, admin, "cam")
	tbl := data.Open("cam")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Predicate false branch: row is empty, marker must NOT be set.
	mFalse := bigtable.NewMutation()
	mFalse.Set("cf", "marker", 1_000_000, []byte("false-branch"))
	mTrue := bigtable.NewMutation()
	mTrue.Set("cf", "marker", 1_000_000, []byte("true-branch"))
	cond := bigtable.ValueFilter("exists")
	var matched bool
	err := tbl.Apply(ctx, "camrow", bigtable.NewCondMutation(cond, mTrue, mFalse), bigtable.GetCondMutationResult(&matched))
	if err != nil {
		t.Fatalf("CheckAndMutateRow: %v", err)
	}
	if matched {
		t.Error("predicate matched on empty row, want false")
	}
	row, err := tbl.ReadRow(ctx, "camrow")
	if err != nil {
		t.Fatalf("ReadRow: %v", err)
	}
	if got := string(row["cf"][0].Value); got != "false-branch" {
		t.Errorf("false-branch value = %q, want false-branch", got)
	}

	// Predicate true branch: cf:marker now exists.
	mTrue2 := bigtable.NewMutation()
	mTrue2.Set("cf", "flag", 2_000_000, []byte("true-applied"))
	cond2 := bigtable.ValueFilter("false-branch")
	var matched2 bool
	err = tbl.Apply(ctx, "camrow", bigtable.NewCondMutation(cond2, mTrue2, nil), bigtable.GetCondMutationResult(&matched2))
	if err != nil {
		t.Fatalf("CheckAndMutateRow 2: %v", err)
	}
	matched = matched2
	if !matched {
		t.Error("predicate should have matched, want true")
	}
	row, err = tbl.ReadRow(ctx, "camrow")
	if err != nil {
		t.Fatalf("ReadRow 2: %v", err)
	}
	found := false
	for _, item := range row["cf"] {
		if item.Column == "cf:flag" && string(item.Value) == "true-applied" {
			found = true
		}
	}
	if !found {
		t.Errorf("true-branch mutation not applied: %v", row["cf"])
	}
}

func TestBigtableMutateRowsBatch(t *testing.T) {
	data, admin := bigtableClients(t)
	mustTable(t, admin, "batch")
	tbl := data.Open("batch")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	rowKeys := []string{"brow-1", "brow-2", "brow-3"}
	muts := make([]*bigtable.Mutation, len(rowKeys))
	for i, rk := range rowKeys {
		m := bigtable.NewMutation()
		m.Set("cf", "q", 1_000_000, []byte("val-"+rk))
		muts[i] = m
	}
	errs, err := tbl.ApplyBulk(ctx, rowKeys, muts)
	if err != nil {
		t.Fatalf("ApplyBulk: %v", err)
	}
	for i, e := range errs {
		if e != nil {
			t.Errorf("ApplyBulk entry %d: %v", i, e)
		}
	}
	for _, rk := range rowKeys {
		row, err := tbl.ReadRow(ctx, rk)
		if err != nil {
			t.Fatalf("ReadRow %s: %v", rk, err)
		}
		if got := string(row["cf"][0].Value); got != "val-"+rk {
			t.Errorf("row %s value = %q, want val-%s", rk, got, rk)
		}
	}
}

func TestBigtableDeleteMutations(t *testing.T) {
	data, admin := bigtableClients(t)
	mustTable(t, admin, "del")
	tbl := data.Open("del")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	m := bigtable.NewMutation()
	m.Set("cf", "keep", 1_000_000, []byte("k"))
	m.Set("cf", "drop", 1_000_000, []byte("d"))
	if err := tbl.Apply(ctx, "drow", m); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	del := bigtable.NewMutation()
	del.DeleteCellsInColumn("cf", "drop")
	if err := tbl.Apply(ctx, "drow", del); err != nil {
		t.Fatalf("Apply delete column: %v", err)
	}
	row, err := tbl.ReadRow(ctx, "drow")
	if err != nil {
		t.Fatalf("ReadRow: %v", err)
	}
	if len(row["cf"]) != 1 || row["cf"][0].Column != "cf:keep" {
		t.Errorf("after column delete: %v, want only cf:keep", row["cf"])
	}

	delFam := bigtable.NewMutation()
	delFam.DeleteCellsInFamily("cf")
	if err := tbl.Apply(ctx, "drow", delFam); err != nil {
		t.Fatalf("Apply delete family: %v", err)
	}
	row, err = tbl.ReadRow(ctx, "drow")
	if err != nil {
		t.Fatalf("ReadRow 2: %v", err)
	}
	if len(row) != 0 {
		t.Errorf("after family delete: %v, want empty row", row)
	}

	// DeleteFromRow via DeleteRow.
	delRow := bigtable.NewMutation()
	delRow.DeleteRow()
	if err := tbl.Apply(ctx, "drow", delRow); err != nil {
		t.Fatalf("Apply delete row: %v", err)
	}
	row, err = tbl.ReadRow(ctx, "drow")
	if err != nil {
		t.Fatalf("ReadRow 3: %v", err)
	}
	if len(row) != 0 {
		t.Errorf("after row delete: %v, want empty", row)
	}
}

func TestBigtableAdminLifecycle(t *testing.T) {
	_, admin := bigtableClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := admin.CreateTable(ctx, "life"); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if err := admin.CreateTable(ctx, "life"); status.Code(err) != codes.AlreadyExists {
		t.Errorf("recreate = %v, want AlreadyExists", err)
	}
	tables, err := admin.Tables(ctx)
	if err != nil {
		t.Fatalf("Tables: %v", err)
	}
	found := false
	for _, tbl := range tables {
		if tbl == "life" {
			found = true
		}
	}
	if !found {
		t.Errorf("Tables() = %v, want life listed", tables)
	}
	if err := admin.CreateColumnFamily(ctx, "life", "cf2"); err != nil {
		t.Fatalf("CreateColumnFamily: %v", err)
	}
	if err := admin.DeleteColumnFamily(ctx, "life", "cf2"); err != nil {
		t.Fatalf("DeleteColumnFamily: %v", err)
	}
	if err := admin.DeleteTable(ctx, "life"); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}
	tables, err = admin.Tables(ctx)
	if err != nil {
		t.Fatalf("Tables 2: %v", err)
	}
	for _, tbl := range tables {
		if tbl == "life" {
			t.Errorf("life still listed after delete: %v", tables)
		}
	}
}

func TestBigtableReadRowsLimitEndsCleanly(t *testing.T) {
	data, admin := bigtableClients(t)
	mustTable(t, admin, "lim")
	tbl := data.Open("lim")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for _, rk := range []string{"lim-a", "lim-b", "lim-c", "lim-d"} {
		m := bigtable.NewMutation()
		m.Set("cf", "q", 1_000_000, []byte("v-"+rk))
		if err := tbl.Apply(ctx, rk, m); err != nil {
			t.Fatalf("Apply %s: %v", rk, err)
		}
	}

	// A limited scan that exhausts the limit mid-table must end the stream
	// cleanly (nil error) — ReadRow wraps every read in LimitRows(1).
	var got []string
	err := tbl.ReadRows(ctx, bigtable.InfiniteRange(""), func(r bigtable.Row) bool {
		got = append(got, r.Key())
		return true
	}, bigtable.LimitRows(2))
	if err != nil {
		t.Fatalf("limited ReadRows: %v", err)
	}
	if len(got) != 2 || got[0] != "lim-a" || got[1] != "lim-b" {
		t.Errorf("limited rows = %v, want [lim-a lim-b]", got)
	}

	// Same through ReadRow (LimitRows(1) under the hood) on a row that is
	// not the first in the table.
	row, err := tbl.ReadRow(ctx, "lim-d")
	if err != nil {
		t.Fatalf("ReadRow lim-d: %v", err)
	}
	if row.Key() != "lim-d" {
		t.Errorf("ReadRow key = %q, want lim-d", row.Key())
	}

	// Limit larger than the table also ends cleanly.
	var all []string
	err = tbl.ReadRows(ctx, bigtable.InfiniteRange(""), func(r bigtable.Row) bool {
		all = append(all, r.Key())
		return true
	}, bigtable.LimitRows(99))
	if err != nil {
		t.Fatalf("over-limit ReadRows: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("over-limit rows = %v, want all 4", all)
	}
}

func TestBigtableReverseScan(t *testing.T) {
	data, admin := bigtableClients(t)
	mustTable(t, admin, "rev")
	tbl := data.Open("rev")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for _, rk := range []string{"rev-a", "rev-b", "rev-c", "rev-d"} {
		m := bigtable.NewMutation()
		m.Set("cf", "q", 1_000_000, []byte("v-"+rk))
		if err := tbl.Apply(ctx, rk, m); err != nil {
			t.Fatalf("Apply %s: %v", rk, err)
		}
	}

	var got []string
	err := tbl.ReadRows(ctx, bigtable.InfiniteRange(""), func(r bigtable.Row) bool {
		got = append(got, r.Key())
		return true
	}, bigtable.ReverseScan())
	if err != nil {
		t.Fatalf("ReverseScan: %v", err)
	}
	if len(got) != 4 || got[0] != "rev-d" || got[1] != "rev-c" || got[2] != "rev-b" || got[3] != "rev-a" {
		t.Errorf("reverse rows = %v, want strictly descending", got)
	}

	// Reversed + row limit combined: first N rows in descending order.
	var limited []string
	err = tbl.ReadRows(ctx, bigtable.InfiniteRange(""), func(r bigtable.Row) bool {
		limited = append(limited, r.Key())
		return true
	}, bigtable.ReverseScan(), bigtable.LimitRows(2))
	if err != nil {
		t.Fatalf("reversed limited ReadRows: %v", err)
	}
	if len(limited) != 2 || limited[0] != "rev-d" || limited[1] != "rev-c" {
		t.Errorf("reversed limited rows = %v, want [rev-d rev-c]", limited)
	}
}

func TestBigtableReadRowsRowList(t *testing.T) {
	data, admin := bigtableClients(t)
	mustTable(t, admin, "rlist")
	tbl := data.Open("rlist")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for _, rk := range []string{"a", "b", "c", "d"} {
		m := bigtable.NewMutation()
		m.Set("cf", "q", 1_000_000, []byte("v-"+rk))
		if err := tbl.Apply(ctx, rk, m); err != nil {
			t.Fatalf("Apply %s: %v", rk, err)
		}
	}

	// Explicit RowList including a missing key: only existing listed rows
	// are returned, in table (ascending) order.
	var got []string
	err := tbl.ReadRows(ctx, bigtable.RowList{"c", "missing", "a"}, func(r bigtable.Row) bool {
		got = append(got, r.Key())
		return true
	})
	if err != nil {
		t.Fatalf("RowList read: %v", err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Errorf("RowList rows = %v, want [a c]", got)
	}
}
