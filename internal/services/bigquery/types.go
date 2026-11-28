package bigquery

import (
	"regexp"
	"time"
)

// Rejects hostile ids before they reach SQLite DDL/DML interpolation.
var validIdent = regexp.MustCompile(`^[A-Za-z0-9_\-]+$`)

const (
	kindKey   = "kind"
	tablesSeg = "tables"
)

type dataset struct {
	ID         string    `json:"datasetId"`
	Kind       string    `json:"kind"`
	CreateTime time.Time `json:"creationTime"`
}

type table struct {
	ID         string         `json:"tableId"`
	DatasetID  string         `json:"datasetId"`
	Kind       string         `json:"kind"`
	Schema     tableSchema    `json:"schema"`
	CreateTime time.Time      `json:"creationTime"`
	Raw        map[string]any `json:"-"`
}

type tableSchema struct {
	Fields []schemaField `json:"fields"`
}

type schemaField struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Mode string `json:"mode,omitempty"`
}

type insertAllRequest struct {
	Rows []struct {
		InsertID string         `json:"insertId"`
		JSON     map[string]any `json:"json"`
	} `json:"rows"`
}

type queryRequest struct {
	Query     string `json:"query"`
	UseLegacy *bool  `json:"useLegacySql,omitempty"`
}

type queryResponseSchema struct {
	Fields []schemaField `json:"fields"`
}

type queryResponse struct {
	JobComplete bool                `json:"jobComplete"`
	Schema      queryResponseSchema `json:"schema"`
	Rows        []row               `json:"rows"`
	TotalRows   string              `json:"totalRows"`
}

type row struct {
	F []cell `json:"f"`
}

type cell struct {
	V any `json:"v"`
}
