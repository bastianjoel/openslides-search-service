package meta

import (
	"bufio"
	"io"
	"sort"

	"gopkg.in/yaml.v3"
)

// Collection is part of the meta model.
type Collection struct {
	Fields map[string]*Member
	Order  int32
}

// CollectionRelation describes a related collection
type CollectionRelation struct {
	Type       string                         `json:"type"`
	Collection *string                        `json:"collection,omitempty"`
	Fields     map[string]*CollectionRelation `json:"fields"`
}

// CollectionDescription is the collection format for search filters
type CollectionDescription struct {
	Searchable []string                      `yaml:"searchable"`
	Additional []string                      `yaml:"additional"`
	Relations  map[string]CollectionRelation `yaml:"relations,omitempty"`
}

// Collections is part of the meta model.
type Collections map[string]*Collection

// UnmarshalYAML implements [gopkg.in/yaml.v3.Unmarshaler].
func (m *Collection) UnmarshalYAML(value *yaml.Node) error {
	m.Order = modelNum.Add(1)
	return value.Decode(&m.Fields)
}

// OrderedKeys returns the keys in document order.
func (m *Collection) OrderedKeys() []string {
	fields := make([]string, 0, len(m.Fields))
	for f := range m.Fields {
		fields = append(fields, f)
	}

	sort.Slice(fields, func(i, j int) bool {
		return m.Fields[fields[i]].Order < m.Fields[fields[j]].Order
	})
	return fields
}

// OrderedKeys returns the keys in document order.
func (ms Collections) OrderedKeys() []string {
	keys := make([]string, 0, len(ms))
	for k := range ms {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return ms[keys[i]].Order < ms[keys[j]].Order
	})
	return keys
}

// Clone returns a deep copy.
func (m *Collection) Clone() *Collection {
	var fields map[string]*Member
	if m.Fields != nil {
		fields = make(map[string]*Member)
		for k, v := range m.Fields {
			fields[k] = v.Clone()
		}
	}
	return &Collection{
		Fields: fields,
		Order:  m.Order,
	}
}

// Clone returns a deep copy.
func (ms Collections) Clone() Collections {
	cp := make(Collections, len(ms))
	for k, v := range ms {
		cp[k] = v.Clone()
	}
	return cp
}

// Retain removes members that are not marked to be kept by the keep function.
func (ms Collections) Retain(keep func(string, string, *Member) bool) {
	for k, m := range ms {
		for kf, f := range m.Fields {
			if !keep(k, kf, f) {
				delete(m.Fields, kf)
			}
		}
		if len(m.Fields) == 0 {
			// log.Printf("throw away collection '%s'.\n", k)
			delete(ms, k)
		}
	}
}

// AsFilters converts a collection into a filter.
func (ms Collections) AsFilters() Filters {
	keys := ms.OrderedKeys()
	fs := make(Filters, 0, len(keys))
	for _, k := range keys {
		cKeys := ms[k].OrderedKeys()

		items := []string{}
		additional := []string{}
		for _, cKey := range cKeys {
			if ms[k].Fields[cKey].Searchable {
				items = append(items, cKey)
			} else {
				additional = append(additional, cKey)
			}
		}

		fs = append(fs, Filter{Name: k, Items: items, Additional: additional})
	}
	return fs
}

// CollectionRequestFields returns the collections with their requested fields
func (ms Collections) CollectionRequestFields() map[string]map[string]*CollectionRelation {
	collections := map[string]map[string]*CollectionRelation{}

	keys := ms.OrderedKeys()
	for _, k := range keys {
		collections[k] = map[string]*CollectionRelation{}
		for _, field := range ms[k].OrderedKeys() {
			collections[k][field] = ms[k].Fields[field].Relation
		}
	}

	return collections
}

func (fs Filters) Write(w io.Writer) error {
	b := bufio.NewWriter(w)

	content := map[string]CollectionDescription{}
	for i := range fs {
		content[fs[i].Name] = CollectionDescription{Searchable: fs[i].Items, Additional: fs[i].Additional}
	}

	if err := yaml.NewEncoder(b).Encode(content); err != nil {
		return err
	}
	return b.Flush()
}
