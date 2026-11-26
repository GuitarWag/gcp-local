package bigtable

import (
	"context"
	"errors"
	"net/http"
	"sync"

	btapb "cloud.google.com/go/bigtable/admin/apiv2/adminpb"
	btpb "cloud.google.com/go/bigtable/apiv2/bigtablepb"
	rpcstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/state"
)

// Service implements the Bigtable data plane (google.bigtable.v2.Bigtable)
// and table admin (google.bigtable.admin.v2.BigtableTableAdmin) over the
// shared state.Store. ReadModifyWriteRow and the query/change-stream RPCs
// stay deliberately unimplemented.
type Service struct {
	project string
	store   state.Store

	// mu guards per-table write serialization so mutations on the same
	// table are atomic (state.Store has no cross-key transactions).
	mu sync.Mutex
}

func New(store state.Store, cfg *config.Config) (*Service, error) {
	return &Service{project: cfg.Project, store: store}, nil
}

func (s *Service) Name() string { return "bigtable" }

func (s *Service) Register(_ *http.ServeMux) {}

func (s *Service) RegisterGRPC(g *grpc.Server) {
	btpb.RegisterBigtableServer(g, &dataServer{svc: s})
	btapb.RegisterBigtableTableAdminServer(g, &adminServer{svc: s})
}

// dataServer implements google.bigtable.v2.Bigtable.
type dataServer struct {
	btpb.UnimplementedBigtableServer
	svc *Service
}

// adminServer implements google.bigtable.admin.v2.BigtableTableAdmin.
type adminServer struct {
	btapb.UnimplementedBigtableTableAdminServer
	svc *Service
}

// ---- data plane ------------------------------------------------------------

func (d *dataServer) MutateRow(_ context.Context, req *btpb.MutateRowRequest) (*btpb.MutateRowResponse, error) {
	if len(req.Mutations) == 0 {
		return nil, errInvalidArgument("No mutations provided")
	}
	tbl, err := d.svc.loadTable(req.TableName)
	if err != nil {
		return nil, err
	}
	d.svc.mu.Lock()
	defer d.svc.mu.Unlock()
	r, err := d.svc.loadRow(req.TableName, string(req.RowKey))
	if err != nil {
		return nil, err
	}
	if r == nil {
		r = &row{Key: string(req.RowKey), Families: map[string]*family{}}
	}
	if err := applyMutations(tbl, r, req.Mutations); err != nil {
		return nil, err
	}
	if err := d.svc.saveRow(req.TableName, r); err != nil {
		return nil, statusErrorf(codes.Internal, "persist row: %v", err)
	}
	return &btpb.MutateRowResponse{}, nil
}

func (d *dataServer) MutateRows(req *btpb.MutateRowsRequest, stream btpb.Bigtable_MutateRowsServer) error {
	n := 0
	for _, e := range req.Entries {
		n += len(e.Mutations)
	}
	if n == 0 {
		return errInvalidArgument("No mutations provided")
	}
	tbl, err := d.svc.loadTable(req.TableName)
	if err != nil {
		return err
	}
	d.svc.mu.Lock()
	defer d.svc.mu.Unlock()
	res := &btpb.MutateRowsResponse{Entries: make([]*btpb.MutateRowsResponse_Entry, len(req.Entries))}
	for i, entry := range req.Entries {
		code := int32(codes.OK)
		msg := ""
		r, err := d.svc.loadRow(req.TableName, string(entry.RowKey))
		if err != nil {
			code, msg = int32(codes.Internal), err.Error()
		} else {
			if r == nil {
				r = &row{Key: string(entry.RowKey), Families: map[string]*family{}}
			}
			if err := applyMutations(tbl, r, entry.Mutations); err != nil {
				code, msg = int32(codes.Internal), err.Error()
			} else if err := d.svc.saveRow(req.TableName, r); err != nil {
				code, msg = int32(codes.Internal), err.Error()
			}
		}
		res.Entries[i] = &btpb.MutateRowsResponse_Entry{
			Index:  int64(i),
			Status: &rpcstatus.Status{Code: code, Message: msg},
		}
	}
	return stream.Send(res)
}

func (d *dataServer) CheckAndMutateRow(_ context.Context, req *btpb.CheckAndMutateRowRequest) (*btpb.CheckAndMutateRowResponse, error) {
	tbl, err := d.svc.loadTable(req.TableName)
	if err != nil {
		return nil, err
	}
	d.svc.mu.Lock()
	defer d.svc.mu.Unlock()
	r, err := d.svc.loadRow(req.TableName, string(req.RowKey))
	if err != nil {
		return nil, err
	}
	if r == nil {
		r = &row{Key: string(req.RowKey), Families: map[string]*family{}}
	}
	res := &btpb.CheckAndMutateRowResponse{}
	which := false
	if req.PredicateFilter == nil {
		which = !r.isEmpty()
	} else {
		nr := r.copy()
		match, err := filterRow(req.PredicateFilter, nr)
		if err != nil {
			return nil, err
		}
		which = match && !nr.isEmpty()
	}
	res.PredicateMatched = which
	muts := req.FalseMutations
	if which {
		muts = req.TrueMutations
	}
	if err := applyMutations(tbl, r, muts); err != nil {
		return nil, err
	}
	if err := d.svc.saveRow(req.TableName, r); err != nil {
		return nil, statusErrorf(codes.Internal, "persist row: %v", err)
	}
	return res, nil
}

// rowSetMatcher builds a key matcher from a RowSet: empty RowSet matches
// everything; otherwise a key matches if it is listed in RowKeys or falls in
// any RowRange.
func rowSetMatcher(rs *btpb.RowSet) func(string) bool {
	if rs == nil || (len(rs.RowKeys) == 0 && len(rs.RowRanges) == 0) {
		return func(string) bool { return true }
	}
	keys := make(map[string]bool, len(rs.RowKeys))
	for _, k := range rs.RowKeys {
		keys[string(k)] = true
	}
	ranges := rs.RowRanges
	return func(key string) bool {
		if keys[key] {
			return true
		}
		for _, rr := range ranges {
			if rowInRange(rr, key) {
				return true
			}
		}
		return false
	}
}

// rowInRange reports whether key falls in rr (start inclusive/exclusive per
// oneof kind, same for end).
func rowInRange(rr *btpb.RowRange, key string) bool {
	okStart, okEnd := true, true
	switch s := rr.StartKey.(type) {
	case *btpb.RowRange_StartKeyClosed:
		okStart = key >= string(s.StartKeyClosed)
	case *btpb.RowRange_StartKeyOpen:
		okStart = key > string(s.StartKeyOpen)
	}
	switch e := rr.EndKey.(type) {
	case *btpb.RowRange_EndKeyClosed:
		okEnd = key <= string(e.EndKeyClosed)
	case *btpb.RowRange_EndKeyOpen:
		okEnd = key < string(e.EndKeyOpen)
	}
	return okStart && okEnd
}

func (d *dataServer) ReadRows(req *btpb.ReadRowsRequest, stream btpb.Bigtable_ReadRowsServer) error {
	if _, err := d.svc.loadTable(req.TableName); err != nil {
		return err
	}
	if err := validateRowRanges(req); err != nil {
		return err
	}
	limit := int(req.RowsLimit)
	match := rowSetMatcher(req.GetRows())
	sentCount := 0
	var sendErr error

	// Iterate rows in key order — descending when Reversed is set, matching
	// bttest — and stop early once the row limit is reached. Reaching the
	// limit is a normal end of stream (no error), like the real service.
	iterate := d.svc.forEachRow
	if req.Reversed {
		iterate = d.svc.forEachRowDesc
	}
	err := iterate(req.TableName, match, func(r *row) error {
		if sendErr != nil || (limit > 0 && sentCount >= limit) {
			return errStop
		}
		nr := r.copy()
		matches, ferr := filterRow(req.Filter, nr)
		if ferr != nil {
			return ferr
		}
		if !matches || nr.isEmpty() {
			return nil
		}
		if err := streamRowChunks(stream, nr); err != nil {
			sendErr = err
			return errStop
		}
		sentCount++
		return nil
	})
	if sendErr != nil {
		return sendErr
	}
	if err != nil && !errors.Is(err, errStop) {
		return err
	}
	return nil
}

// errStop stops forEachRow iteration without surfacing an error.
var errStop = errors.New("stop")

func (d *dataServer) SampleRowKeys(req *btpb.SampleRowKeysRequest, stream btpb.Bigtable_SampleRowKeysServer) error {
	if _, err := d.svc.loadTable(req.TableName); err != nil {
		return err
	}
	keys, err := d.svc.rowKeys(req.TableName)
	if err != nil {
		return err
	}
	var offset int64
	for i, k := range keys {
		r, err := d.svc.loadRow(req.TableName, k)
		if err != nil {
			return err
		}
		if r == nil {
			continue
		}
		// Real service: at least the final row key, others random.
		if i == len(keys)-1 || sampleRand() < 0.01 {
			if err := stream.Send(&btpb.SampleRowKeysResponse{
				RowKey: []byte(r.Key), OffsetBytes: offset,
			}); err != nil {
				return err
			}
		}
		offset += int64(r.size())
	}
	// Terminator: empty key with total offset.
	return stream.Send(&btpb.SampleRowKeysResponse{RowKey: []byte{}, OffsetBytes: offset})
}

// ---- deliberately unimplemented (asserted in tests/go/stubs_test.go) -------

func (d *dataServer) ReadModifyWriteRow(context.Context, *btpb.ReadModifyWriteRowRequest) (*btpb.ReadModifyWriteRowResponse, error) {
	return nil, statusErrorf(codes.Unimplemented, "ReadModifyWriteRow is not implemented by this emulator")
}

func (d *dataServer) PrepareQuery(context.Context, *btpb.PrepareQueryRequest) (*btpb.PrepareQueryResponse, error) {
	return nil, statusErrorf(codes.Unimplemented, "PrepareQuery is not implemented by this emulator")
}

func (d *dataServer) ExecuteQuery(*btpb.ExecuteQueryRequest, btpb.Bigtable_ExecuteQueryServer) error {
	return statusErrorf(codes.Unimplemented, "ExecuteQuery is not implemented by this emulator")
}

func (d *dataServer) GenerateInitialChangeStreamPartitions(*btpb.GenerateInitialChangeStreamPartitionsRequest, btpb.Bigtable_GenerateInitialChangeStreamPartitionsServer) error {
	return statusErrorf(codes.Unimplemented, "change streams are not implemented by this emulator")
}

func (d *dataServer) ReadChangeStream(*btpb.ReadChangeStreamRequest, btpb.Bigtable_ReadChangeStreamServer) error {
	return statusErrorf(codes.Unimplemented, "change streams are not implemented by this emulator")
}

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
		if create := mod.GetCreate(); create != nil {
			if _, ok := t.meta.Families[mod.Id]; ok {
				return nil, statusErrorf(codes.AlreadyExists, "family %q already exists", mod.Id)
			}
			t.meta.Families[mod.Id] = &familyMeta{
				Name:  req.Name + "/columnFamilies/" + mod.Id,
				Order: t.meta.Counter,
				Gc:    create.GetGcRule(),
				Value: create.GetValueType(),
			}
			t.meta.Counter++
		} else if mod.GetDrop() {
			if _, ok := t.meta.Families[mod.Id]; !ok {
				return nil, errInvalidArgument("can't delete unknown family %q", mod.Id)
			}
			delete(t.meta.Families, mod.Id)
			// Purge all data in that family from every row.
			keys, err := a.svc.rowKeys(req.Name)
			if err != nil {
				return nil, err
			}
			for _, k := range keys {
				r, err := a.svc.loadRow(req.Name, k)
				if err != nil || r == nil {
					continue
				}
				if _, ok := r.Families[mod.Id]; !ok {
					continue
				}
				delete(r.Families, mod.Id)
				if err := a.svc.saveRow(req.Name, r); err != nil {
					return nil, statusErrorf(codes.Internal, "purge family: %v", err)
				}
			}
		} else if update := mod.GetUpdate(); update != nil {
			cf, ok := t.meta.Families[mod.Id]
			if !ok {
				return nil, errInvalidArgument("no such family %q", mod.Id)
			}
			if update.GetGcRule() != nil {
				cf.Gc = update.GetGcRule()
			}
		}
	}
	if err := a.svc.saveTable(t); err != nil {
		return nil, statusErrorf(codes.Internal, "persist table: %v", err)
	}
	return adminTableProto(t), nil
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
