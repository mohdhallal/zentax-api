package spec

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// Load reads and decodes a dataset.json.
//
// Decoding is STRICT in both directions:
//
//   - DisallowUnknownFields — a key the structs do not model is an error, so a
//     dataset that grows a field cannot be silently half-seeded;
//   - UseNumber — numbers inside free-form values (an instance's taxData) keep
//     their exact literal, so a value re-marshalled into a PUT body is byte-for
//     byte what the dataset declared.
//
// Load does not validate the content; call Validate for that.
func Load(path string) (*Spec, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("load dataset: %w", err)
	}
	defer func() { _ = file.Close() }()

	spec, err := Decode(file)
	if err != nil {
		return nil, fmt.Errorf("load dataset %s: %w", path, err)
	}
	return spec, nil
}

// Decode is Load from an already-open reader.
func Decode(r io.Reader) (*Spec, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	dec.UseNumber()

	var spec Spec
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	// Reject a second JSON document / trailing garbage.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode: trailing data after the dataset object")
	}
	return &spec, nil
}

// LoadAndValidate is the call a tool wants: read the file, then enforce the
// dataset's own invariants.
func LoadAndValidate(path string) (*Spec, error) {
	spec, err := Load(path)
	if err != nil {
		return nil, err
	}
	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("dataset %s: %w", path, err)
	}
	return spec, nil
}
