package internal

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

func ClearByPath(obj any, path string) error {
	parts := splitPath(path)

	return clearRecursive(reflect.ValueOf(obj), parts)
}

func clearRecursive(v reflect.Value, parts []string) error {
	if len(parts) == 0 {
		return nil
	}

	part := parts[0]
	last := len(parts) == 1

	// Dereference pointers
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}

	switch v.Kind() {

	case reflect.Struct:
		if part == "*" {
			// Apply to all fields
			for i := 0; i < v.NumField(); i++ {
				field := v.Field(i)
				if last {
					field.Set(reflect.Zero(field.Type()))
				} else {
					clearRecursive(field, parts[1:])
				}
			}
			return nil
		}

		field := findStructFieldByJSONTag(v, part)
		if !field.IsValid() {
			return fmt.Errorf("struct field or json tag %q not found", part)
		}

		if last {
			field.Set(reflect.Zero(field.Type()))
			return nil
		}
		return clearRecursive(field, parts[1:])

	case reflect.Map:
		if part == "*" {
			for _, key := range v.MapKeys() {
				elem := v.MapIndex(key)
				if last {
					v.SetMapIndex(key, reflect.Zero(elem.Type()))
				} else {
					clearRecursive(elem, parts[1:])
				}
			}
			return nil
		}

		key := reflect.ValueOf(part)
		elem := v.MapIndex(key)
		if !elem.IsValid() {
			return nil
		}

		if last {
			v.SetMapIndex(key, reflect.Zero(elem.Type()))
			return nil
		}
		return clearRecursive(elem, parts[1:])

	case reflect.Slice, reflect.Array:
		if part == "*" {
			for i := 0; i < v.Len(); i++ {
				elem := v.Index(i)
				if last {
					elem.Set(reflect.Zero(elem.Type()))
				} else {
					clearRecursive(elem, parts[1:])
				}
			}
			return nil
		}

		idx, err := strconv.Atoi(part)
		if err != nil || idx < 0 || idx >= v.Len() {
			return fmt.Errorf("invalid index %q", part)
		}

		elem := v.Index(idx)
		if last {
			elem.Set(reflect.Zero(elem.Type()))
			return nil
		}
		return clearRecursive(elem, parts[1:])

	default:
		return fmt.Errorf("cannot navigate into %s at %q", v.Kind(), part)
	}
}

func findStructFieldByJSONTag(v reflect.Value, name string) reflect.Value {
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)

		// Skip unexported fields
		if sf.PkgPath != "" {
			continue
		}

		tag := sf.Tag.Get("json")
		if tag != "" {
			tagName := strings.Split(tag, ",")[0]
			if tagName == name {
				return v.Field(i)
			}
		}

		// Fallback to Go field name
		if sf.Name == name {
			return v.Field(i)
		}
	}

	return reflect.Value{}
}

func splitPath(path string) []string {
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

	if buf.Len() > 0 {
		parts = append(parts, buf.String())
	}

	return parts
}
