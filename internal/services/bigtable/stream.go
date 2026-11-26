package bigtable

import (
	"bytes"
	"math/rand/v2"

	btpb "cloud.google.com/go/bigtable/apiv2/bigtablepb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// sampleRand is the random source for RowSampleFilter and SampleRowKeys.
var sampleRand = rand.Float64

// streamRowChunks sends one filtered row as a ReadRowsResponse using the
// CellChunk protocol: one chunk per cell, the last chunk carries CommitRow.
func streamRowChunks(stream btpb.Bigtable_ReadRowsServer, r *row) error {
	resp := &btpb.ReadRowsResponse{}
	for _, fam := range r.sortedFamilies() {
		for _, colName := range fam.ColNames {
			cells := fam.Cells[colName]
			if len(cells) == 0 {
				continue
			}
			for _, c := range cells {
				resp.Chunks = append(resp.Chunks, &btpb.ReadRowsResponse_CellChunk{
					RowKey:          []byte(r.Key),
					FamilyName:      &wrapperspb.StringValue{Value: fam.Name},
					Qualifier:       &wrapperspb.BytesValue{Value: []byte(colName)},
					TimestampMicros: c.Ts,
					Value:           c.Value,
					Labels:          c.Labels,
				})
			}
		}
	}
	if len(resp.Chunks) == 0 {
		return nil
	}
	// The final chunk must carry the commit flag.
	last := resp.Chunks[len(resp.Chunks)-1]
	last.RowStatus = &btpb.ReadRowsResponse_CellChunk_CommitRow{CommitRow: true}
	return stream.Send(resp)
}

// validateRowRanges rejects structurally invalid RowRange oneof usage the way
// the real service does (see bttest/validation.go).
func validateRowRanges(req *btpb.ReadRowsRequest) error {
	rowRanges := req.GetRows().GetRowRanges()
	if len(rowRanges) == 0 {
		return nil
	}
	for i, rr := range rowRanges {
		skC := rr.GetStartKeyClosed()
		skO := rr.GetStartKeyOpen()
		ekC := rr.GetEndKeyClosed()
		ekO := rr.GetEndKeyOpen()
		switch msg := rangeValidationMessage(skC, skO, ekC, ekO); {
		case msg != "":
			return errInvalidArgument("Error in element #%d: %s", i, msg)
		}
	}
	return nil
}

// rangeValidationMessage mirrors bttest's messageOnInvalidKeyRanges.
func rangeValidationMessage(startClosed, startOpen, endClosed, endOpen []byte) string {
	switch {
	case len(startClosed) != 0 && len(startOpen) != 0:
		return "both start_key_closed and start_key_open cannot be set"
	case len(endClosed) != 0 && len(endOpen) != 0:
		return "both end_key_closed and end_key_open cannot be set"
	case keysOutOfRange(startClosed, endClosed):
		return "start_key_closed must be less than end_key_closed"
	case keysOutOfRange(startOpen, endOpen):
		return "start_key_open must be less than end_key_open"
	case keysOutOfRange(startClosed, endOpen):
		return "start_key_closed must be less than end_key_open"
	case keysOutOfRange(startOpen, endClosed):
		return "start_key_open must be less than end_key_closed"
	}
	return ""
}

// keysOutOfRange reports whether both keys are set and start > end; unset
// keys (either or both) are implicit/explicit indefinite ranges.
func keysOutOfRange(start, end []byte) bool {
	if len(start) == 0 || len(end) == 0 {
		return false
	}
	return bytes.Compare(start, end) > 0
}
