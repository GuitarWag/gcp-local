package bigquery

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func (s *Service) handleDatasets(w http.ResponseWriter, r *http.Request, parts []string) {
	switch len(parts) {
	case 2:
		if r.Method == http.MethodPost {
			s.createDataset(w, r)
			return
		}
		if r.Method == http.MethodGet {
			s.listDatasets(w)
			return
		}
	case 3:
		s.datasetItem(w, r, parts[2])
		return
	case 4:
		if parts[3] == tablesSeg {
			s.tableCollection(w, r, parts[2])
			return
		}
	case 5:
		if parts[3] == tablesSeg {
			s.tableItem(w, r, parts[2], parts[4])
			return
		}
	case 6:
		if parts[3] == tablesSeg && parts[5] == "insertAll" {
			s.insertAll(w, r, parts[2], parts[4])
			return
		}
	}
	http.NotFound(w, r)
}

func (s *Service) createDataset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DatasetReference struct {
			DatasetID string `json:"datasetId"`
		} `json:"datasetReference"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	id := body.DatasetReference.DatasetID
	if id == "" {
		s.writeErr(w, http.StatusBadRequest, "datasetId required")
		return
	}
	if !validIdent.MatchString(id) {
		s.writeErr(w, http.StatusBadRequest, "invalid datasetId")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.datasets[id]; ok {
		s.writeErr(w, http.StatusConflict, "dataset exists")
		return
	}
	d := &dataset{ID: id, Kind: "bigquery#dataset", CreateTime: time.Now().UTC()}
	s.datasets[id] = d
	s.tables[id] = map[string]*table{}
	s.writeJSON(w, http.StatusOK, d)
}

func (s *Service) listDatasets(w http.ResponseWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []*dataset{}
	for _, d := range s.datasets {
		items = append(items, d)
	}
	s.writeJSON(w, http.StatusOK, map[string]any{kindKey: "bigquery#datasetList", "datasets": items})
}

func (s *Service) datasetItem(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.datasets[id]
	if !ok {
		s.writeErr(w, http.StatusNotFound, "dataset not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.writeJSON(w, http.StatusOK, d)
	case http.MethodDelete:
		delete(s.datasets, id)
		for tableID := range s.tables[id] {
			_, _ = s.db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %q", id+"__"+tableID))
		}
		delete(s.tables, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// knownDatasets returns the set of dataset IDs registered on the service,
// used to disambiguate `dataset.table` from `alias.column` in queries.
func (s *Service) knownDatasets() map[string]struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]struct{}, len(s.datasets))
	for id := range s.datasets {
		out[id] = struct{}{}
	}
	return out
}
