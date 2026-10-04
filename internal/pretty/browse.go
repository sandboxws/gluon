package pretty

// Browse flattens a value into headers and rows for a scrollable view.
//
// It is deliberately not the same code path as Rich. Rich caps at maxRows
// because scrollback is a finite thing a person scrolls past; a browser has no
// such limit and should show everything the child actually sent. What it cannot
// show is what the child never sent — the encoder caps a collection at 200
// items — so Omitted reports that rather than letting the view imply it holds
// the whole thing.
func Browse(v Value) (headers []string, rows [][]string, omitted int, ok bool) {
	v = v.resolveIDs()

	// A pointer to a collection browses as the collection.
	if v.Kind == "ptr" && len(v.Items) == 1 {
		v = v.Items[0]
	}

	switch v.Kind {
	case "list":
		if len(v.Items) == 0 {
			return nil, nil, 0, false
		}
		// A list of structs earns a column per field, which is the whole
		// reason to open a table over one.
		if names, uniform := structColumns(v.Items); uniform {
			headers = append([]string{"#"}, names...)
			for i, it := range v.Items {
				row := []string{itoa(i)}
				for _, n := range names {
					row = append(row, fieldCell(it, n))
				}
				rows = append(rows, row)
			}
			return headers, rows, v.More, true
		}
		headers = []string{"#", "value"}
		for i, it := range v.Items {
			rows = append(rows, []string{itoa(i), it.inline()})
		}
		return headers, rows, v.More, true

	case "map":
		if len(v.Keys) == 0 {
			return nil, nil, 0, false
		}
		headers = []string{"key", "value"}
		for i, k := range v.Keys {
			rows = append(rows, []string{k.inline(), v.Items[i].inline()})
		}
		return headers, rows, v.More, true

	case "struct":
		if len(v.Fields) == 0 {
			return nil, nil, 0, false
		}
		headers = []string{"field", "value"}
		for _, f := range v.Fields {
			rows = append(rows, []string{f.Name, f.Val.inline()})
		}
		return headers, rows, 0, true
	}
	return nil, nil, 0, false
}

// structColumns is the shared field names of a list whose elements are all
// structs with the same shape. Anything else returns uniform=false, because a
// column that exists for some rows and not others is a worse table than one
// column of rendered values.
func structColumns(items []Value) (names []string, uniform bool) {
	for i, it := range items {
		s := it
		if s.Kind == "ptr" && len(s.Items) == 1 {
			s = s.Items[0]
		}
		if s.Kind != "struct" || len(s.Fields) == 0 {
			return nil, false
		}
		if i == 0 {
			for _, f := range s.Fields {
				names = append(names, f.Name)
			}
			continue
		}
		if len(s.Fields) != len(names) {
			return nil, false
		}
		for j, f := range s.Fields {
			if f.Name != names[j] {
				return nil, false
			}
		}
	}
	return names, len(names) > 0
}

// fieldCell is one struct field rendered inline, or empty when the element does
// not carry it.
func fieldCell(v Value, name string) string {
	if v.Kind == "ptr" && len(v.Items) == 1 {
		v = v.Items[0]
	}
	for _, f := range v.Fields {
		if f.Name == name {
			return f.Val.inline()
		}
	}
	return ""
}
