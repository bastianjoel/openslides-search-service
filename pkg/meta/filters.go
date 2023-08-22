package meta

import (
	"log"
	"sort"

	"gopkg.in/yaml.v3"
)

// Filter is part of the meta model.
type Filter struct {
	Name       string
	Items      []string
	Additional []string
	Relations  map[string]*CollectionRelation
}

// FilterKey is part of the meta model.
type FilterKey struct {
	Name  string
	Order int32
}

// Filters is a list of filters.
type Filters []Filter

// UnmarshalYAML implements [gopkg.in/yaml.v3.Unmarshaler].
func (fk *FilterKey) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	*fk = FilterKey{
		Order: filterNum.Add(1),
		Name:  s,
	}
	return nil
}

// UnmarshalYAML implements [gopkg.in/yaml.v3.Unmarshaler].
func (fs *Filters) UnmarshalYAML(value *yaml.Node) error {
	var fsm map[FilterKey]CollectionDescription
	if err := value.Decode(&fsm); err != nil {
		return err
	}
	sorted := make([]FilterKey, 0, len(fsm))
	for k := range fsm {
		sorted = append(sorted, k)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Order < sorted[j].Order
	})

	*fs = make(Filters, 0, len(sorted))
	for _, s := range sorted {
		relations := map[string]*CollectionRelation{}
		for k, r := range fsm[s].Relations {
			relations[k] = r
		}

		*fs = append(*fs, Filter{
			Name:       s.Name,
			Items:      fsm[s].Searchable,
			Additional: fsm[s].Additional,
			Relations:  relations,
		})
	}
	return nil
}

// Retain returns a keep function for [Retain] which also updates
// if Members are searchable and adds their relation informations
func (fs Filters) Retain(verbose bool) func(string, string, *Member) bool {
	type key struct {
		rel   string
		field string
	}
	keep := map[key]struct{}{}
	additional := map[key]struct{}{}
	relations := map[key]*CollectionRelation{}
	for _, m := range fs {
		for _, f := range m.Items {
			keep[key{rel: m.Name, field: f}] = struct{}{}
		}

		for _, f := range m.Additional {
			additional[key{rel: m.Name, field: f}] = struct{}{}
		}

		for f, data := range m.Relations {
			relations[key{rel: m.Name, field: f}] = data
		}
	}
	return func(rk, fk string, m *Member) bool {
		if _, ok := relations[key{rel: rk, field: fk}]; ok {
			m.Relation = relations[key{rel: rk, field: fk}]
		}

		if _, ok := additional[key{rel: rk, field: fk}]; ok {
			m.Searchable = false
			return true
		}

		_, ok := keep[key{rel: rk, field: fk}]
		if !ok && verbose {
			log.Printf("removing filtered %s.%s\n", rk, fk)
		} else {
			m.Searchable = true
		}

		return ok
	}
}
