package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
)

const maxKeyErrors = 32

func decode(b []byte) (*File, error) {
	if err := checkKeys(b); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var f File
	if err := dec.Decode(&f); err != nil {
		return nil, decodeError(err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("%w: data after the top-level object", ErrInvalid)
	}
	return &f, nil
}

func decodeError(err error) error {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		key := te.Field
		if key == "" {
			key = "(top level)"
		}
		return bad(key, "cannot use %s here, want %s", te.Value, typeName(te.Type))
	}
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return fmt.Errorf("%w: JSON syntax error at byte %d: %v", ErrInvalid, se.Offset, err)
	}
	return fmt.Errorf("%w: %v", ErrInvalid, err)
}

func typeName(t reflect.Type) string {
	switch {
	case t == nil:
		return "another type"
	case t == durationType:
		return "a duration string such as \"5s\""
	case t.Kind() == reflect.String:
		return "a string"
	case t.Kind() == reflect.Bool:
		return "true or false"
	case t.Kind() >= reflect.Int && t.Kind() <= reflect.Float64:
		return "an integer"
	case t.Kind() == reflect.Slice:
		return "a list"
	case t.Kind() == reflect.Struct:
		return "an object"
	}
	return t.String()
}

type keyWalker struct {
	dec  *json.Decoder
	errs []error
}

func checkKeys(b []byte) error {
	w := &keyWalker{dec: json.NewDecoder(bytes.NewReader(b))}
	w.dec.UseNumber()
	if err := w.value(reflect.TypeFor[File](), ""); err != nil {
		return decodeError(err)
	}
	return errors.Join(w.errs...)
}

func (w *keyWalker) fail(err error) {
	if len(w.errs) < maxKeyErrors {
		w.errs = append(w.errs, err)
	}
}

func (w *keyWalker) value(t reflect.Type, path string) error {
	tok, err := w.dec.Token()
	if err != nil {
		return err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch {
	case d == '{' && t != nil && t.Kind() == reflect.Struct:
		return w.object(t, path)
	case d == '[' && t != nil && t.Kind() == reflect.Slice:
		return w.list(t.Elem(), path)
	}
	return w.skip()
}

func (w *keyWalker) object(t reflect.Type, path string) error {
	seen := make(map[string]bool)
	for w.dec.More() {
		tok, err := w.dec.Token()
		if err != nil {
			return err
		}
		name, _ := tok.(string)
		key := joinKey(path, name)
		if seen[name] {
			w.fail(bad(key, "given twice"))
		}
		seen[name] = true
		ft, known := fieldType(t, name)
		if !known {
			w.fail(bad(key, "unknown key"))
		}
		if err := w.value(ft, key); err != nil {
			return err
		}
	}
	_, err := w.dec.Token()
	return err
}

func (w *keyWalker) list(elem reflect.Type, path string) error {
	for i := 0; w.dec.More(); i++ {
		if err := w.value(elem, path+"["+strconv.Itoa(i)+"]"); err != nil {
			return err
		}
	}
	_, err := w.dec.Token()
	return err
}

func (w *keyWalker) skip() error {
	depth := 1
	for depth > 0 {
		tok, err := w.dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
}

func fieldType(t reflect.Type, name string) (reflect.Type, bool) {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		tag, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
		if tag == name {
			return sf.Type, true
		}
	}
	return nil, false
}

func joinKey(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}
