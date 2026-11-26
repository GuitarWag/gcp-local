package spanner

// admin.go: google.spanner.database.v1 DatabaseAdmin surface. Only what
// client libraries need to provision databases is implemented:
// CreateDatabase / UpdateDatabaseDdl / DropDatabase / GetDatabase /
// GetDatabaseDdl. The LRO-returning calls respond with already-complete
// operations so clients never need an Operations server to poll.

import (
	"context"
	"fmt"
	"strings"
	"time"

	adminpb "cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	longrunningpb "google.golang.org/genproto/googleapis/longrunning"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// adminServer implements adminpb.DatabaseAdminServer.
type adminServer struct {
	adminpb.UnimplementedDatabaseAdminServer
	svc *Service
}

func (a *adminServer) CreateDatabase(ctx context.Context, req *adminpb.CreateDatabaseRequest) (*longrunningpb.Operation, error) {
	create := strings.TrimSpace(req.GetCreateStatement())
	if !hasPrefixFold(create, "CREATE DATABASE") {
		return nil, errInvalidArgument("CreateDatabase requires a CREATE DATABASE statement, got: %s", firstLine(create))
	}
	name := unquoteIdent(strings.TrimSpace(create[len("CREATE DATABASE"):]))
	if name == "" {
		return nil, errInvalidArgument("missing database name in CREATE DATABASE")
	}
	dbPath := fmt.Sprintf("%s/databases/%s", req.GetParent(), name)
	if err := a.svc.engine.createDatabase(dbPath); err != nil {
		return nil, err
	}
	// Apply extra statements (CREATE TABLE etc.) through the data path.
	for _, stmt := range req.GetExtraStatements() {
		if err := a.svc.execAdminDDL(dbPath, stmt); err != nil {
			_ = a.svc.engine.dropDatabase(dbPath)
			return nil, err
		}
	}
	db := &adminpb.Database{
		Name:                   dbPath,
		State:                  adminpb.Database_READY,
		CreateTime:             timestamppb.New(nowUTC()),
		VersionRetentionPeriod: "1h",
	}
	resp, err := anypb.New(db)
	if err != nil {
		return nil, errInternal("marshal database: %v", err)
	}
	return &longrunningpb.Operation{
		Name:   fmt.Sprintf("%s/operations/_create_%d", dbPath, a.svc.nextSeq()),
		Done:   true,
		Result: &longrunningpb.Operation_Response{Response: resp},
	}, nil
}

func (a *adminServer) UpdateDatabaseDdl(ctx context.Context, req *adminpb.UpdateDatabaseDdlRequest) (*longrunningpb.Operation, error) {
	dbPath := req.GetDatabase()
	if !a.svc.engine.hasDatabase(dbPath) {
		return nil, errNotFound("Database not found: %s", dbPath)
	}
	for _, stmt := range req.GetStatements() {
		if err := a.svc.execAdminDDL(dbPath, stmt); err != nil {
			return nil, err
		}
	}
	resp, err := anypb.New(&emptypb.Empty{})
	if err != nil {
		return nil, errInternal("marshal ddl response: %v", err)
	}
	return &longrunningpb.Operation{
		Name:   fmt.Sprintf("%s/operations/_ddl_%d", dbPath, a.svc.nextSeq()),
		Done:   true,
		Result: &longrunningpb.Operation_Response{Response: resp},
	}, nil
}

func (a *adminServer) GetDatabase(ctx context.Context, req *adminpb.GetDatabaseRequest) (*adminpb.Database, error) {
	if !a.svc.engine.hasDatabase(req.GetName()) {
		return nil, errNotFound("Database not found: %s", req.GetName())
	}
	return &adminpb.Database{
		Name:                   req.GetName(),
		State:                  adminpb.Database_READY,
		CreateTime:             timestamppb.New(nowUTC()),
		VersionRetentionPeriod: "1h",
	}, nil
}

func (a *adminServer) GetDatabaseDdl(ctx context.Context, req *adminpb.GetDatabaseDdlRequest) (*adminpb.GetDatabaseDdlResponse, error) {
	if !a.svc.engine.hasDatabase(req.GetDatabase()) {
		return nil, errNotFound("Database not found: %s", req.GetDatabase())
	}
	return &adminpb.GetDatabaseDdlResponse{Statements: a.svc.engine.ddlLog(req.GetDatabase())}, nil
}

func (a *adminServer) DropDatabase(ctx context.Context, req *adminpb.DropDatabaseRequest) (*emptypb.Empty, error) {
	if err := a.svc.engine.dropDatabase(req.GetDatabase()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// execAdminDDL runs one DDL statement through the same parser/executor the
// data path uses (via a synthetic session record).
func (s *Service) execAdminDDL(database, stmt string) error {
	sess := &sessionRecord{database: database}
	rs, err := s.runSQLArgs(sess, stmt, nil, nil)
	if err != nil {
		return err
	}
	_ = rs
	s.engine.recordDDL(database, stmt)
	return nil
}

func (s *Service) nextSeq() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return int(s.seq) //nolint:gosec // G115: monotonic counter, never near MaxInt
}

func nowUTC() time.Time { return time.Now().UTC() }
