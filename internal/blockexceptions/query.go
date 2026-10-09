package blockexceptions

import "slices"

// Selection modes describe selection, not fuzzy/exact string operators.
type SearchMode int

const (
	Partial    SearchMode = 1
	Unselected SearchMode = 2
	All        SearchMode = 3
)

type Selection struct {
	ValueType  ValueType
	SearchMode SearchMode
	ValueList  []string
}
type Sort struct {
	Field     string
	Direction int
}

// Query is structural input only. Pointers distinguish absence from explicit zero
// or an empty date. Nil and empty collections are preserved, not interpreted.
type Query struct {
	Keyword         string
	AppTypeList     []int
	DataSrcList     []string
	SearchValueList []Selection
	SortField       []Sort
	PageIdx         *int
	PageSize        *int
	CreateStartTime *string
	CreateEndTime   *string
	ModifyStartTime *string
	ModifyEndTime   *string
}

// ValidatedQuery is not a query plan and must not be used as a rule matcher.
type ValidatedQuery struct {
	Keyword         string
	AppTypeList     []int
	DataSrcList     []string
	SearchValueList []Selection
	SortField       []Sort
	PageIdx         int
	PageSize        int
	Offset          int
}

func pageValue(v *int, fallback int, field string) (int, error) {
	if v == nil {
		return fallback, nil
	}
	if *v == -1 {
		return 0, fail(field, Unsupported)
	}
	if *v <= 0 {
		return 0, fail(field, Invalid)
	}
	return *v, nil
}
func ValidateQuery(p Policy, q Query) (ValidatedQuery, error) {
	if err := p.Validate(); err != nil {
		return ValidatedQuery{}, err
	}
	for _, d := range []struct {
		field string
		value *string
	}{{"createStartTime", q.CreateStartTime}, {"createEndTime", q.CreateEndTime}, {"modifyStartTime", q.ModifyStartTime}, {"modifyEndTime", q.ModifyEndTime}} {
		if d.value != nil {
			return ValidatedQuery{}, fail(d.field, Unsupported)
		}
	}
	if err := textLength(q.Keyword, "keyword", 0, 50); err != nil {
		return ValidatedQuery{}, err
	}
	// Subtract from a remaining budget, avoiding addition overflow.
	remaining := p.MaxListItems
	take := func(n int) bool {
		if n > remaining {
			return false
		}
		remaining -= n
		return true
	}
	for _, n := range []int{len(q.AppTypeList), len(q.DataSrcList), len(q.SearchValueList), len(q.SortField)} {
		if !take(n) {
			return ValidatedQuery{}, fail("collections", BudgetExceeded)
		}
	}
	for _, s := range q.SearchValueList {
		if !take(len(s.ValueList)) {
			return ValidatedQuery{}, fail("searchValueList", BudgetExceeded)
		}
	}
	apps := map[int]bool{}
	for _, a := range q.AppTypeList {
		if a < 1 || a > 3 {
			return ValidatedQuery{}, fail("appTypeList", Invalid)
		}
		if apps[a] {
			return ValidatedQuery{}, fail("appTypeList", Duplicate)
		}
		apps[a] = true
	}
	sources := map[string]bool{}
	for _, s := range q.DataSrcList {
		if err := opaque(s, "dataSrcList", p.MaxIDBytes); err != nil {
			return ValidatedQuery{}, err
		}
		if sources[s] {
			return ValidatedQuery{}, fail("dataSrcList", Duplicate)
		}
		sources[s] = true
	}
	types := map[ValueType]bool{}
	for _, s := range q.SearchValueList {
		if err := validValueType(s.ValueType, "searchValueList.valueType"); err != nil {
			return ValidatedQuery{}, err
		}
		if types[s.ValueType] {
			return ValidatedQuery{}, fail("searchValueList.valueType", Duplicate)
		}
		types[s.ValueType] = true
		if s.SearchMode < Partial || s.SearchMode > All {
			return ValidatedQuery{}, fail("searchValueList.searchMode", Invalid)
		}
		values := map[string]bool{}
		for _, v := range s.ValueList {
			// Values are opaque query tokens; do not infer filtering semantics or
			// canonicalize IP selections into an execution plan.
			if err := opaque(v, "searchValueList.valueList", p.MaxUserValueBytes); err != nil {
				return ValidatedQuery{}, err
			}
			if values[v] {
				return ValidatedQuery{}, fail("searchValueList.valueList", Duplicate)
			}
			values[v] = true
		}
	}
	fields := map[string]bool{}
	for _, s := range q.SortField {
		if s.Field != "createTime" && s.Field != "modifyTime" {
			return ValidatedQuery{}, fail("sortField.field", Invalid)
		}
		if s.Direction != 1 && s.Direction != -1 {
			return ValidatedQuery{}, fail("sortField.direction", Invalid)
		}
		if fields[s.Field] {
			return ValidatedQuery{}, fail("sortField.field", Duplicate)
		}
		fields[s.Field] = true
	}
	idx, err := pageValue(q.PageIdx, 1, "pageIdx")
	if err != nil {
		return ValidatedQuery{}, err
	}
	size, err := pageValue(q.PageSize, 10, "pageSize")
	if err != nil {
		return ValidatedQuery{}, err
	}
	if size > p.MaxPageSize {
		return ValidatedQuery{}, fail("pageSize", BudgetExceeded)
	}
	maxInt := int(^uint(0) >> 1)
	if idx-1 > maxInt/size {
		return ValidatedQuery{}, fail("offset", BudgetExceeded)
	}
	out := ValidatedQuery{Keyword: q.Keyword, AppTypeList: slices.Clone(q.AppTypeList), DataSrcList: slices.Clone(q.DataSrcList), SearchValueList: slices.Clone(q.SearchValueList), SortField: slices.Clone(q.SortField), PageIdx: idx, PageSize: size, Offset: (idx - 1) * size}
	for i := range out.SearchValueList {
		out.SearchValueList[i].ValueList = slices.Clone(q.SearchValueList[i].ValueList)
	}
	return out, nil
}
