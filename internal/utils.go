package internal

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// ClearByPath zeroes the value or values that path selects within obj, which
// must be a non-nil pointer.
//
// A path is a dot-separated list of parts. Each part names a struct field (by
// its JSON name or its Go name), a map key, or a slice index; "*" selects every
// field, map value, or element. Single quotes delimit a part that itself
// contains dots.
//
// Data that happens to be absent — a nil pointer, a missing map key, an index
// past the end of a slice — is not an error: there is nothing to clear. A path
// the type can never satisfy is an error whatever the data holds, exactly as
// ValidatePath reports it.
func ClearByPath(obj any, path string) error {
	parts, err := splitPath(path)
	if err != nil {
		return err
	}

	v := reflect.ValueOf(obj)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("cannot clear within %T: not a non-nil pointer", obj)
	}
	if err := validateParts(v.Type(), parts); err != nil {
		return err
	}
	return clearParts(v, parts)
}

// ValidatePath reports whether path can select anything within a value of type
// t. It lets a configured path be rejected once at startup, rather than
// silently clearing nothing on every snapshot.
func ValidatePath(t reflect.Type, path string) error {
	parts, err := splitPath(path)
	if err != nil {
		return err
	}
	return validateParts(t, parts)
}

func validateParts(t reflect.Type, parts []string) error {
	if len(parts) == 0 {
		return nil
	}
	part, rest := parts[0], parts[1:]

	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	switch t.Kind() {

	case reflect.Struct:
		if part == "*" {
			if len(rest) == 0 {
				return nil
			}
			// The rest of the path only has to apply to one field; clearParts
			// skips the fields it does not apply to.
			for i := 0; i < t.NumField(); i++ {
				if sf := t.Field(i); sf.IsExported() && validateParts(sf.Type, rest) == nil {
					return nil
				}
			}
			return fmt.Errorf("no field of %s accepts %q", t, strings.Join(rest, "."))
		}

		index, ok := fieldIndex(t, part)
		if !ok {
			return fmt.Errorf("%s has no field %q", t, part)
		}
		return validateParts(t.FieldByIndex(index).Type, rest)

	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return fmt.Errorf("cannot look up %q in %s: its keys are not strings", part, t)
		}
		return validateParts(t.Elem(), rest)

	case reflect.Slice, reflect.Array:
		if part != "*" {
			if _, err := parseIndex(part); err != nil {
				return fmt.Errorf("%q is not an index into %s: use a number or *", part, t)
			}
		}
		return validateParts(t.Elem(), rest)

	default:
		return fmt.Errorf("cannot navigate into %s at %q", t.Kind(), part)
	}
}

func clearParts(v reflect.Value, parts []string) error {
	part, rest := parts[0], parts[1:]

	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}

	switch v.Kind() {

	case reflect.Struct:
		if part == "*" {
			for i := 0; i < v.NumField(); i++ {
				field := v.Field(i)
				if !field.CanSet() {
					continue // unexported
				}
				if len(rest) > 0 && validateParts(field.Type(), rest) != nil {
					continue // the rest of the path does not apply to this field
				}
				if err := clearValue(field, rest); err != nil {
					return err
				}
			}
			return nil
		}

		index, ok := fieldIndex(v.Type(), part)
		if !ok {
			return fmt.Errorf("%s has no field %q", v.Type(), part)
		}
		field, err := v.FieldByIndexErr(index)
		if err != nil {
			return nil // reached through a nil embedded pointer
		}
		return clearValue(field, rest)

	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("cannot look up %q in %s: its keys are not strings", part, v.Type())
		}
		if part == "*" {
			for _, key := range v.MapKeys() {
				if err := clearMapValue(v, key, rest); err != nil {
					return err
				}
			}
			return nil
		}
		return clearMapValue(v, reflect.ValueOf(part).Convert(v.Type().Key()), rest)

	case reflect.Slice, reflect.Array:
		if part == "*" {
			for i := 0; i < v.Len(); i++ {
				if err := clearValue(v.Index(i), rest); err != nil {
					return err
				}
			}
			return nil
		}

		idx, err := parseIndex(part)
		if err != nil {
			return fmt.Errorf("%q is not an index into %s: use a number or *", part, v.Type())
		}
		if idx >= v.Len() {
			return nil
		}
		return clearValue(v.Index(idx), rest)

	default:
		return fmt.Errorf("cannot navigate into %s at %q", v.Kind(), part)
	}
}

// clearValue zeroes v when the path ends at it and keeps walking otherwise.
func clearValue(v reflect.Value, rest []string) error {
	if len(rest) == 0 {
		v.SetZero()
		return nil
	}
	return clearParts(v, rest)
}

// clearMapValue applies the rest of the path to the value stored under key.
// Map values are not addressable, so the value is copied out, changed, and
// stored back.
func clearMapValue(m, key reflect.Value, rest []string) error {
	elem := m.MapIndex(key)
	if !elem.IsValid() {
		return nil
	}

	value := reflect.New(m.Type().Elem()).Elem()
	value.Set(elem)
	if err := clearValue(value, rest); err != nil {
		return err
	}
	m.SetMapIndex(key, value)
	return nil
}

// fieldIndex locates the struct field that name refers to: a field of t by its
// JSON name or Go name, or failing that a field of one of t's embedded structs.
// encoding/json marshals an embedded struct's fields as though they belonged to
// the outer struct, so that is how they appear in the data a path is written
// against.
func fieldIndex(t reflect.Type, name string) ([]int, bool) {
	var embedded []reflect.StructField

	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}

		jsonName, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
		if sf.Name == name || (jsonName != "" && jsonName == name) {
			return sf.Index, true
		}
		if sf.Anonymous && jsonName == "" {
			embedded = append(embedded, sf)
		}
	}

	for _, sf := range embedded {
		et := sf.Type
		if et.Kind() == reflect.Pointer {
			et = et.Elem()
		}
		if et.Kind() != reflect.Struct {
			continue
		}
		if index, ok := fieldIndex(et, name); ok {
			return append(slices.Clone(sf.Index), index...), true
		}
	}

	return nil, false
}

func parseIndex(part string) (int, error) {
	idx, err := strconv.Atoi(part)
	if err != nil || idx < 0 {
		return 0, fmt.Errorf("invalid index %q", part)
	}
	return idx, nil
}

func splitPath(path string) ([]string, error) {
	var parts []string
	var buf strings.Builder
	inQuotes := false

	for i := 0; i < len(path); i++ {
		c := path[i]

		switch c {
		case '\'':
			inQuotes = !inQuotes

		case '.':
			if !inQuotes {
				parts = append(parts, buf.String())
				buf.Reset()
				continue
			}
			buf.WriteByte(c)

		default:
			buf.WriteByte(c)
		}
	}
	parts = append(parts, buf.String())

	if inQuotes {
		return nil, fmt.Errorf("unterminated quote in path %q", path)
	}
	if slices.Contains(parts, "") {
		return nil, fmt.Errorf("empty part in path %q", path)
	}
	return parts, nil
}
