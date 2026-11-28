package bigtable

import (
	"context"

	btapb "cloud.google.com/go/bigtable/admin/apiv2/adminpb"
	btpb "cloud.google.com/go/bigtable/apiv2/bigtablepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ---- table admin (gRPC) -----------------------------------------------------

func (a *adminServer) CreateTable(_ context.Context, req *btapb.CreateTableRequest) (*btapb.Table, error) {
	a.svc.mu.Lock()
	defer a.svc.mu.Unlock()
	tbl := req.Parent + "/tables/" + req.TableId
	if _, err := a.svc.loadTable(tbl); err == nil {
		return nil, statusErrorf(codes.AlreadyExists, "table %q already exists", tbl)
	}
	t := &table{meta: tableMeta{Name: tbl, Families: map[string]*familyMeta{}}}
	if req.GetTable() != nil {
		for id, cf := range req.GetTable().GetColumnFamilies() {
			t.meta.Families[id] = &familyMeta{
				Name:  tbl + "/columnFamilies/" + id,
				Order: t.meta.Counter,
				Gc:    cf.GetGcRule(),
				Value: cf.GetValueType(),
			}
			t.meta.Counter++
		}
	}
	if err := a.svc.saveTable(t); err != nil {
		return nil, statusErrorf(codes.Internal, "persist table: %v", err)
	}
	return adminTableProto(t), nil
}

func (a *adminServer) ListTables(_ context.Context, req *btapb.ListTablesRequest) (*btapb.ListTablesResponse, error) {
	tables, err := a.svc.listTables(req.Parent)
	if err != nil {
		return nil, err
	}
	res := &btapb.ListTablesResponse{}
	for _, t := range tables {
		res.Tables = append(res.Tables, &btapb.Table{
			Name:           t.meta.Name,
			ColumnFamilies: adminFamiliesProto(t),
			Granularity:    btapb.Table_MILLIS,
		})
	}
	return res, nil
}

func (a *adminServer) GetTable(_ context.Context, req *btapb.GetTableRequest) (*btapb.Table, error) {
	t, err := a.svc.loadTable(req.Name)
	if err != nil {
		return nil, err
	}
	return adminTableProto(t), nil
}

func (a *adminServer) DeleteTable(_ context.Context, req *btapb.DeleteTableRequest) (*emptypb.Empty, error) {
	a.svc.mu.Lock()
	defer a.svc.mu.Unlock()
	if err := a.svc.deleteTable(req.Name); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (a *adminServer) ModifyColumnFamilies(_ context.Context, req *btapb.ModifyColumnFamiliesRequest) (*btapb.Table, error) {
	a.svc.mu.Lock()
	defer a.svc.mu.Unlock()
	t, err := a.svc.loadTable(req.Name)
	if err != nil {
		return nil, err
	}
	for _, mod := range req.Modifications {
		if err := a.applyFamilyMod(t, req.Name, mod); err != nil {
			return nil, err
		}
	}
	if err := a.svc.saveTable(t); err != nil {
		return nil, statusErrorf(codes.Internal, "persist table: %v", err)
	}
	return adminTableProto(t), nil
}

// applyFamilyMod applies one column-family modification to t. The branch
// order matches the real API: create, then drop, then update.
func (a *adminServer) applyFamilyMod(t *table, name string, mod *btapb.ModifyColumnFamiliesRequest_Modification) error {
	if create := mod.GetCreate(); create != nil {
		if _, ok := t.meta.Families[mod.Id]; ok {
			return statusErrorf(codes.AlreadyExists, "family %q already exists", mod.Id)
		}
		t.meta.Families[mod.Id] = &familyMeta{
			Name:  name + "/columnFamilies/" + mod.Id,
			Order: t.meta.Counter,
			Gc:    create.GetGcRule(),
			Value: create.GetValueType(),
		}
		t.meta.Counter++
		return nil
	}
	if mod.GetDrop() {
		if _, ok := t.meta.Families[mod.Id]; !ok {
			return errInvalidArgument("can't delete unknown family %q", mod.Id)
		}
		delete(t.meta.Families, mod.Id)
		return a.purgeFamily(name, mod.Id)
	}
	if update := mod.GetUpdate(); update != nil {
		cf, ok := t.meta.Families[mod.Id]
		if !ok {
			return errInvalidArgument("no such family %q", mod.Id)
		}
		if update.GetGcRule() != nil {
			cf.Gc = update.GetGcRule()
		}
	}
	return nil
}

// purgeFamily removes family and all its cells from every row of name.
func (a *adminServer) purgeFamily(name, family string) error {
	keys, err := a.svc.rowKeys(name)
	if err != nil {
		return err
	}
	for _, k := range keys {
		r, err := a.svc.loadRow(name, k)
		if err != nil || r == nil {
			continue
		}
		if _, ok := r.Families[family]; !ok {
			continue
		}
		delete(r.Families, family)
		if err := a.svc.saveRow(name, r); err != nil {
			return statusErrorf(codes.Internal, "purge family: %v", err)
		}
	}
	return nil
}

func (a *adminServer) DropRowRange(_ context.Context, req *btapb.DropRowRangeRequest) (*emptypb.Empty, error) {
	a.svc.mu.Lock()
	defer a.svc.mu.Unlock()
	if _, err := a.svc.loadTable(req.Name); err != nil {
		return nil, err
	}
	start, end := "", ""
	deleteAll := req.GetDeleteAllDataFromTable()
	if !deleteAll {
		if p := req.GetRowKeyPrefix(); p != nil {
			start = string(p)
			end = prefixSuccessor(start)
		} else {
			return nil, errInvalidArgument("DropRowRange needs a prefix or delete_all_data_from_table")
		}
	}
	keys, err := a.svc.rowKeys(req.Name)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		if !deleteAll && !rowInRange(&btpb.RowRange{
			StartKey: &btpb.RowRange_StartKeyClosed{StartKeyClosed: []byte(start)},
			EndKey:   &btpb.RowRange_EndKeyOpen{EndKeyOpen: []byte(end)},
		}, k) {
			continue
		}
		if deleteAll {
			if err := a.svc.store.Delete(namespaceRows, rowKey(req.Name, k)); err != nil {
				return nil, err
			}
			continue
		}
		r, err := a.svc.loadRow(req.Name, k)
		if err != nil || r == nil {
			continue
		}
		r.Families = map[string]*family{}
		if err := a.svc.saveRow(req.Name, r); err != nil {
			return nil, statusErrorf(codes.Internal, "drop range: %v", err)
		}
	}
	return &emptypb.Empty{}, nil
}

// adminTableProto renders table metadata for admin responses.
func adminTableProto(t *table) *btapb.Table {
	return &btapb.Table{
		Name:           t.meta.Name,
		ColumnFamilies: adminFamiliesProto(t),
		Granularity:    btapb.Table_MILLIS,
	}
}

func adminFamiliesProto(t *table) map[string]*btapb.ColumnFamily {
	out := make(map[string]*btapb.ColumnFamily, len(t.meta.Families))
	for id, fm := range t.meta.Families {
		out[id] = &btapb.ColumnFamily{GcRule: fm.Gc, ValueType: fm.Value}
	}
	return out
}

// prefixSuccessor returns the smallest key greater than every key with the
// given prefix ("" when the prefix is the whole keyspace).
func prefixSuccessor(prefix string) string {
	if prefix == "" {
		return ""
	}
	n := len(prefix)
	for n > 0 && prefix[n-1] == '\xff' {
		n-- // walk back over trailing 0xff bytes
	}
	n--
	if n < 0 {
		return ""
	}
	ans := []byte(prefix[:n])
	ans = append(ans, prefix[n]+1)
	return string(ans)
}
