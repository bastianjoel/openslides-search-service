package meta

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Fields is part of the meta model.
type Fields struct {
	Type string `yaml:"type"`
	To   string `yaml:"to"`
}

// UnmarshalYAML implements [gopkg.in/yaml.v3.Unmarshaler].
func (fs *Fields) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err == nil {
		fs.Type = s
		return nil
	}
	var field struct {
		Type string `yaml:"type"`
		To   string `yaml:"to"`
	}
	if err := value.Decode(&field); err != nil {
		return fmt.Errorf("fields object without type: %w", err)
	}
	fs.Type = field.Type
	fs.To = field.To
	return nil
}

// Clone returns a deep copy.
func (fs *Fields) Clone() *Fields {
	if fs == nil {
		return nil
	}
	return &Fields{
		Type: fs.Type,
		To:   fs.To,
	}
}
