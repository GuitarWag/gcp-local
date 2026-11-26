package bigtable

import (
	"bytes"
	"regexp"
	"sort"

	btpb "cloud.google.com/go/bigtable/apiv2/bigtablepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func errInvalidArgument(format string, args ...any) error {
	return status.Errorf(codes.InvalidArgument, format, args...)
}

func errNotFound(format string, args ...any) error {
	return status.Errorf(codes.NotFound, format, args...)
}

// newRegexp compiles a fully-anchored RE2 pattern as the real service does.
func newRegexp(pat []byte) (*regexp.Regexp, error) {
	return regexp.Compile("^(?:" + string(pat) + ")$")
}

// filterRow applies a RowFilter to a row in place, returning whether at least
// one cell matched. Port of the real service semantics (see bttest/inmem.go).
func filterRow(f *btpb.RowFilter, r *row) (bool, error) {
	if f == nil {
		return true, nil
	}
	switch f := f.Filter.(type) {
	case *btpb.RowFilter_BlockAllFilter:
		if !f.BlockAllFilter {
			return false, errInvalidArgument("block_all_filter must be true if set")
		}
		return false, nil
	case *btpb.RowFilter_PassAllFilter:
		if !f.PassAllFilter {
			return false, errInvalidArgument("pass_all_filter must be true if set")
		}
		return true, nil
	case *btpb.RowFilter_Chain_:
		if len(f.Chain.Filters) < 2 {
			return false, errInvalidArgument("Chain must contain at least two RowFilters")
		}
		for _, sub := range f.Chain.Filters {
			match, err := filterRow(sub, r)
			if err != nil {
				return false, err
			}
			if !match {
				return false, nil
			}
		}
		return true, nil
	case *btpb.RowFilter_Interleave_:
		if len(f.Interleave.Filters) < 2 {
			return false, errInvalidArgument("Interleave must contain at least two RowFilters")
		}
		srs := make([]*row, 0, len(f.Interleave.Filters))
		for _, sub := range f.Interleave.Filters {
			sr := r.copy()
			match, err := filterRow(sub, sr)
			if err != nil {
				return false, err
			}
			if match {
				srs = append(srs, sr)
			}
		}
		r.Families = make(map[string]*family)
		var count int
		for _, sr := range srs {
			for _, fam := range sr.Families {
				dst := r.getOrCreateFamily(fam.Name, fam.Order)
				for colName, cs := range fam.Cells {
					dst.Cells[colName] = append(dst.CellsByColumn(colName), cs...)
				}
			}
		}
		for _, fam := range r.Families {
			for _, cs := range fam.Cells {
				sort.SliceStable(cs, func(i, j int) bool { return cs[i].Ts > cs[j].Ts })
				count += len(cs)
			}
		}
		return count > 0, nil
	case *btpb.RowFilter_CellsPerColumnLimitFilter:
		lim := int(f.CellsPerColumnLimitFilter)
		if lim <= 0 {
			return false, errInvalidArgument("Error in field 'cells_per_column_limit_filter' : argument must be > 0")
		}
		for _, fam := range r.Families {
			for col, cs := range fam.Cells {
				if len(cs) > lim {
					fam.Cells[col] = cs[:lim]
				}
			}
		}
		return true, nil
	case *btpb.RowFilter_Condition_:
		match, err := filterRow(f.Condition.PredicateFilter, r.copy())
		if err != nil {
			return false, err
		}
		if match {
			if f.Condition.TrueFilter == nil {
				return false, nil
			}
			return filterRow(f.Condition.TrueFilter, r)
		}
		if f.Condition.FalseFilter == nil {
			return false, nil
		}
		return filterRow(f.Condition.FalseFilter, r)
	case *btpb.RowFilter_RowKeyRegexFilter:
		if len(f.RowKeyRegexFilter) == 0 {
			return false, errInvalidArgument("Error in field 'row_key_regex_filter' : argument must not be empty")
		}
		rx, err := newRegexp(f.RowKeyRegexFilter)
		if err != nil {
			return false, errInvalidArgument("Error in field 'row_key_regex_filter' : %v", err)
		}
		return rx.MatchString(r.Key), nil
	case *btpb.RowFilter_CellsPerRowLimitFilter:
		lim := int(f.CellsPerRowLimitFilter)
		if lim <= 0 {
			return false, errInvalidArgument("Error in field 'cells_per_row_limit_filter' : argument must be > 0")
		}
		// Deterministic order: families in creation order, then each family's
		// sorted qualifier index (matching bttest). Columns trimmed to zero
		// cells are dropped from both the cell map and the index.
		for _, fam := range r.sortedFamilies() {
			for _, col := range append([]string(nil), fam.ColNames...) {
				if lim <= 0 {
					delete(fam.Cells, col)
					fam.ColNames = removeSorted(fam.ColNames, col)
					continue
				}
				cs := fam.Cells[col]
				if len(cs) > lim {
					fam.Cells[col] = cs[:lim]
					lim = 0
				} else {
					lim -= len(cs)
				}
			}
		}
		return true, nil
	case *btpb.RowFilter_CellsPerRowOffsetFilter:
		offset := int(f.CellsPerRowOffsetFilter)
		for _, fam := range r.sortedFamilies() {
			for _, col := range append([]string(nil), fam.ColNames...) {
				cs := fam.Cells[col]
				if len(cs) > offset {
					fam.Cells[col] = cs[offset:]
					return true, nil
				}
				delete(fam.Cells, col)
				fam.ColNames = removeSorted(fam.ColNames, col)
				offset -= len(cs)
			}
		}
		return false, nil
	case *btpb.RowFilter_RowSampleFilter:
		if f.RowSampleFilter <= 0.0 || f.RowSampleFilter >= 1.0 {
			return false, errInvalidArgument("row_sample_filter argument must be between 0.0 and 1.0")
		}
		return sampleRand() < f.RowSampleFilter, nil
	}

	// Per-cell filters: include/modify each cell independently.
	cellCount := 0
	for _, fam := range r.Families {
		for colName, cs := range fam.Cells {
			filtered, err := filterCells(f, fam.Name, colName, cs)
			if err != nil {
				return false, err
			}
			fam.Cells[colName] = filtered
			cellCount += len(fam.Cells[colName])
		}
	}
	return cellCount > 0, nil
}

// filterCells applies an include/modify filter to each cell of one column.
func filterCells(f *btpb.RowFilter, fam, col string, cs []cell) ([]cell, error) {
	var ret []cell
	for _, c := range cs {
		include, err := includeCell(f, fam, col, c)
		if err != nil {
			return nil, err
		}
		if include {
			c, err = modifyCell(f, c)
			if err != nil {
				return nil, err
			}
			ret = append(ret, c)
		}
	}
	return ret, nil
}

// modifyCell applies value-transforming filters to a cell.
func modifyCell(f *btpb.RowFilter, c cell) (cell, error) {
	if f == nil {
		return c, nil
	}
	switch filter := f.Filter.(type) {
	case *btpb.RowFilter_StripValueTransformer:
		return cell{Ts: c.Ts}, nil
	case *btpb.RowFilter_ApplyLabelTransformer:
		if !validLabelRe.MatchString(filter.ApplyLabelTransformer) {
			return cell{}, errInvalidArgument(
				`apply_label_transformer must match RE2([a-z0-9\-]+), but found %v`,
				filter.ApplyLabelTransformer)
		}
		return cell{Ts: c.Ts, Value: c.Value, Labels: []string{filter.ApplyLabelTransformer}}, nil
	default:
		return c, nil
	}
}

var validLabelRe = regexp.MustCompile(`^[a-z0-9\-]{1,15}$`)

// includeCell decides whether one cell passes an include-type filter.
func includeCell(f *btpb.RowFilter, fam, col string, c cell) (bool, error) {
	if f == nil {
		return true, nil
	}
	switch f := f.Filter.(type) {
	case *btpb.RowFilter_CellsPerColumnLimitFilter,
		*btpb.RowFilter_RowKeyRegexFilter,
		*btpb.RowFilter_StripValueTransformer,
		*btpb.RowFilter_ApplyLabelTransformer:
		// Row-level or cell-modifying: no per-cell exclusion here.
		return true, nil
	case *btpb.RowFilter_FamilyNameRegexFilter:
		rx, err := newRegexp([]byte(f.FamilyNameRegexFilter))
		if err != nil {
			return false, errInvalidArgument("Error in field 'family_name_regex_filter' : %v", err)
		}
		return rx.MatchString(fam), nil
	case *btpb.RowFilter_ColumnQualifierRegexFilter:
		rx, err := newRegexp(f.ColumnQualifierRegexFilter)
		if err != nil {
			return false, errInvalidArgument("Error in field 'column_qualifier_regex_filter' : %v", err)
		}
		return rx.MatchString(col), nil
	case *btpb.RowFilter_ValueRegexFilter:
		rx, err := newRegexp(f.ValueRegexFilter)
		if err != nil {
			return false, errInvalidArgument("Error in field 'value_regex_filter' : %v", err)
		}
		return rx.Match(c.Value), nil
	case *btpb.RowFilter_ColumnRangeFilter:
		if fam != f.ColumnRangeFilter.FamilyName {
			return false, nil
		}
		inStart := func() bool { return col >= "" }
		switch sq := f.ColumnRangeFilter.StartQualifier.(type) {
		case *btpb.ColumnRange_StartQualifierOpen:
			inStart = func() bool { return col > string(sq.StartQualifierOpen) }
		case *btpb.ColumnRange_StartQualifierClosed:
			inStart = func() bool { return col >= string(sq.StartQualifierClosed) }
		}
		inEnd := func() bool { return true }
		switch eq := f.ColumnRangeFilter.EndQualifier.(type) {
		case *btpb.ColumnRange_EndQualifierClosed:
			inEnd = func() bool { return col <= string(eq.EndQualifierClosed) }
		case *btpb.ColumnRange_EndQualifierOpen:
			inEnd = func() bool { return col < string(eq.EndQualifierOpen) }
		}
		return inStart() && inEnd(), nil
	case *btpb.RowFilter_TimestampRangeFilter:
		// [start, end): lower inclusive, upper exclusive (0 = unbounded).
		return c.Ts >= f.TimestampRangeFilter.StartTimestampMicros &&
			(f.TimestampRangeFilter.EndTimestampMicros == 0 ||
				c.Ts < f.TimestampRangeFilter.EndTimestampMicros), nil
	case *btpb.RowFilter_ValueRangeFilter:
		v := c.Value
		inStart := func() bool { return len(v) >= 0 }
		switch sv := f.ValueRangeFilter.StartValue.(type) {
		case *btpb.ValueRange_StartValueOpen:
			inStart = func() bool { return bytes.Compare(v, sv.StartValueOpen) > 0 }
		case *btpb.ValueRange_StartValueClosed:
			inStart = func() bool { return bytes.Compare(v, sv.StartValueClosed) >= 0 }
		}
		inEnd := func() bool { return true }
		switch ev := f.ValueRangeFilter.EndValue.(type) {
		case *btpb.ValueRange_EndValueClosed:
			inEnd = func() bool { return bytes.Compare(v, ev.EndValueClosed) <= 0 }
		case *btpb.ValueRange_EndValueOpen:
			inEnd = func() bool { return bytes.Compare(v, ev.EndValueOpen) < 0 }
		}
		return inStart() && inEnd(), nil
	default:
		return true, nil
	}
}
