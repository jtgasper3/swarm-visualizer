package internal

import (
	"reflect"
	"strings"
	"testing"
)

type clrInner struct {
	Secret string `json:"secret"`
	Keep   string `json:"keep"`
}

// ClrMeta is embedded in clrOuter, the way the Docker API types embed their
// metadata, so its fields marshal as though they were declared on clrOuter.
type ClrMeta struct {
	Version string
	Notes   map[string]string
}

type clrOuter struct {
	ClrMeta
	hidden string
	Name   string              `json:"name"`
	Inner  clrInner            `json:"inner"`
	Items  []clrInner          `json:"items"`
	M      map[string]clrInner `json:"m"`
	Labels map[string]string   `json:"labels"`
	Dotted map[string]string   `json:"dotted"`
}

func newClrOuter() *clrOuter {
	return &clrOuter{
		ClrMeta: ClrMeta{Version: "v1", Notes: map[string]string{"n": "v"}},
		hidden:  "h",
		Name:    "name",
		Inner:   clrInner{Secret: "s", Keep: "keep"},
		Items:   []clrInner{{Secret: "s0", Keep: "k0"}, {Secret: "s1", Keep: "k1"}},
		M:       map[string]clrInner{"x": {Secret: "sx", Keep: "kx"}},
		Labels:  map[string]string{"secret": "v", "keep": "v"},
		Dotted:  map[string]string{"a.b.c": "v"},
	}
}

func TestClearByPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
		check   func(t *testing.T, o *clrOuter)
	}{
		{
			name: "top-level field by json tag",
			path: "name",
			check: func(t *testing.T, o *clrOuter) {
				if o.Name != "" {
					t.Errorf("Name = %q, want empty", o.Name)
				}
				if o.Inner.Secret != "s" {
					t.Errorf("Inner.Secret = %q, want unchanged", o.Inner.Secret)
				}
			},
		},
		{
			name: "nested struct field",
			path: "inner.secret",
			check: func(t *testing.T, o *clrOuter) {
				if o.Inner.Secret != "" {
					t.Errorf("Inner.Secret = %q, want empty", o.Inner.Secret)
				}
				if o.Inner.Keep != "keep" {
					t.Errorf("Inner.Keep = %q, want unchanged", o.Inner.Keep)
				}
			},
		},
		{
			name: "slice wildcard field",
			path: "items.*.secret",
			check: func(t *testing.T, o *clrOuter) {
				for i, it := range o.Items {
					if it.Secret != "" {
						t.Errorf("Items[%d].Secret = %q, want empty", i, it.Secret)
					}
					if it.Keep == "" {
						t.Errorf("Items[%d].Keep cleared unexpectedly", i)
					}
				}
			},
		},
		{
			name: "slice index field",
			path: "items.0.secret",
			check: func(t *testing.T, o *clrOuter) {
				if o.Items[0].Secret != "" {
					t.Errorf("Items[0].Secret = %q, want empty", o.Items[0].Secret)
				}
				if o.Items[1].Secret != "s1" {
					t.Errorf("Items[1].Secret = %q, want unchanged", o.Items[1].Secret)
				}
			},
		},
		{
			name: "struct wildcard clears all fields",
			path: "inner.*",
			check: func(t *testing.T, o *clrOuter) {
				if o.Inner.Secret != "" || o.Inner.Keep != "" {
					t.Errorf("Inner = %+v, want all fields empty", o.Inner)
				}
			},
		},
		{
			name: "map entry by key",
			path: "labels.secret",
			check: func(t *testing.T, o *clrOuter) {
				if o.Labels["secret"] != "" {
					t.Errorf("Labels[secret] = %q, want empty", o.Labels["secret"])
				}
				if o.Labels["keep"] != "v" {
					t.Errorf("Labels[keep] = %q, want unchanged", o.Labels["keep"])
				}
			},
		},
		{
			name: "map wildcard clears all values",
			path: "m.*",
			check: func(t *testing.T, o *clrOuter) {
				v := o.M["x"]
				if v.Secret != "" || v.Keep != "" {
					t.Errorf("M[x] = %+v, want zero value", v)
				}
			},
		},
		{
			name: "quoted key with embedded dots",
			path: "dotted.'a.b.c'",
			check: func(t *testing.T, o *clrOuter) {
				if o.Dotted["a.b.c"] != "" {
					t.Errorf("Dotted[a.b.c] = %q, want empty", o.Dotted["a.b.c"])
				}
			},
		},
		{
			name: "missing map key is a no-op",
			path: "labels.absent",
			check: func(t *testing.T, o *clrOuter) {
				if o.Labels["secret"] != "v" || o.Labels["keep"] != "v" {
					t.Errorf("Labels mutated unexpectedly: %v", o.Labels)
				}
			},
		},
		{
			name: "field of a map value by key",
			path: "m.x.secret",
			check: func(t *testing.T, o *clrOuter) {
				if v := o.M["x"]; v.Secret != "" || v.Keep != "kx" {
					t.Errorf("M[x] = %+v, want only Secret cleared", v)
				}
			},
		},
		{
			name: "field of every map value",
			path: "m.*.secret",
			check: func(t *testing.T, o *clrOuter) {
				if v := o.M["x"]; v.Secret != "" || v.Keep != "kx" {
					t.Errorf("M[x] = %+v, want only Secret cleared", v)
				}
			},
		},
		{
			name: "field promoted from an embedded struct",
			path: "Version",
			check: func(t *testing.T, o *clrOuter) {
				if o.Version != "" {
					t.Errorf("Version = %q, want empty", o.Version)
				}
				if o.Notes["n"] != "v" {
					t.Errorf("Notes = %v, want unchanged", o.Notes)
				}
			},
		},
		{
			name: "map key under an embedded struct",
			path: "Notes.n",
			check: func(t *testing.T, o *clrOuter) {
				if o.Notes["n"] != "" {
					t.Errorf("Notes[n] = %q, want empty", o.Notes["n"])
				}
			},
		},
		{
			name: "embedded struct by its type name",
			path: "ClrMeta.Version",
			check: func(t *testing.T, o *clrOuter) {
				if o.Version != "" {
					t.Errorf("Version = %q, want empty", o.Version)
				}
			},
		},
		{
			name: "top-level wildcard skips unexported fields",
			path: "*",
			check: func(t *testing.T, o *clrOuter) {
				if o.Name != "" || o.Version != "" || o.Items != nil {
					t.Errorf("exported fields not cleared: %+v", o)
				}
				if o.hidden != "h" {
					t.Errorf("hidden = %q, want unchanged", o.hidden)
				}
			},
		},
		{
			name: "struct wildcard applies the rest where it fits",
			path: "*.secret",
			check: func(t *testing.T, o *clrOuter) {
				if o.Inner.Secret != "" || o.Labels["secret"] != "" {
					t.Errorf("secret not cleared: Inner=%+v Labels=%v", o.Inner, o.Labels)
				}
				if o.Name != "name" || o.Inner.Keep != "keep" || o.Labels["keep"] != "v" {
					t.Errorf("unrelated values changed: %+v", o)
				}
			},
		},
		{
			name: "slice index out of range is a no-op",
			path: "items.5.secret",
			check: func(t *testing.T, o *clrOuter) {
				if o.Items[0].Secret != "s0" || o.Items[1].Secret != "s1" {
					t.Errorf("Items mutated unexpectedly: %+v", o.Items)
				}
			},
		},
		{
			name:    "unknown field errors",
			path:    "nope",
			wantErr: true,
		},
		{
			name:    "unknown field after a wildcard errors",
			path:    "items.*.nope",
			wantErr: true,
		},
		{
			name:    "non-numeric slice index errors",
			path:    "items.first.secret",
			wantErr: true,
		},
		{
			name:    "navigate into scalar errors",
			path:    "name.foo",
			wantErr: true,
		},
		{
			name:    "empty path errors",
			path:    "",
			wantErr: true,
		},
		{
			name:    "empty part errors",
			path:    "inner..secret",
			wantErr: true,
		},
		{
			name:    "trailing dot errors",
			path:    "inner.",
			wantErr: true,
		},
		{
			name:    "unterminated quote errors",
			path:    "dotted.'a.b.c",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := newClrOuter()
			err := ClearByPath(o, tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for path %q, got nil", tc.path)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for path %q: %v", tc.path, err)
			}
			if tc.check != nil {
				tc.check(t, o)
			}
		})
	}
}

func TestClearByPath_RequiresPointer(t *testing.T) {
	if err := ClearByPath(*newClrOuter(), "name"); err == nil {
		t.Error("expected an error for a non-pointer value")
	}
	if err := ClearByPath((*clrOuter)(nil), "name"); err == nil {
		t.Error("expected an error for a nil pointer")
	}
}

func TestClearByPath_AbsentDataIsNotAnError(t *testing.T) {
	type leaf struct{ Secret string }
	type node struct {
		Ptr   *leaf
		Map   map[string]leaf
		Slice []leaf
	}

	for _, path := range []string{"Ptr.Secret", "Map.k.Secret", "Map.*.Secret", "Slice.0.Secret", "Slice.*.Secret"} {
		if err := ClearByPath(&node{}, path); err != nil {
			t.Errorf("ClearByPath(%q) on empty data: %v", path, err)
		}
	}
}

// ValidatePath has to agree with ClearByPath on every path: the same table
// drives both, without and with data.
func TestValidatePath(t *testing.T) {
	tests := []struct {
		path    string
		wantErr string
	}{
		{path: "name"},
		{path: "inner.secret"},
		{path: "inner.*"},
		{path: "items.*.secret"},
		{path: "items.0.secret"},
		{path: "items.5.secret"},
		{path: "m.x.secret"},
		{path: "m.*.secret"},
		{path: "labels.anything"},
		{path: "dotted.'a.b.c'"},
		{path: "Version"},
		{path: "Notes.n"},
		{path: "ClrMeta.Version"},
		{path: "*"},
		{path: "*.secret"},
		{path: "nope", wantErr: `has no field "nope"`},
		{path: "inner.secrte", wantErr: `has no field "secrte"`},
		{path: "items.*.nope", wantErr: `has no field "nope"`},
		{path: "m.*.nope", wantErr: `has no field "nope"`},
		{path: "items.first.secret", wantErr: "use a number or *"},
		{path: "items.-1.secret", wantErr: "use a number or *"},
		{path: "name.foo", wantErr: "cannot navigate into string"},
		{path: "labels.a.b", wantErr: "cannot navigate into string"},
		{path: "hidden", wantErr: `has no field "hidden"`},
		{path: "inner.*.nope", wantErr: "no field of"},
		{path: "", wantErr: "empty part"},
		{path: "inner..secret", wantErr: "empty part"},
		{path: "inner.", wantErr: "empty part"},
		{path: "dotted.'a.b.c", wantErr: "unterminated quote"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			validateErr := ValidatePath(reflect.TypeFor[clrOuter](), tc.path)
			clearErr := ClearByPath(newClrOuter(), tc.path)

			if tc.wantErr == "" {
				if validateErr != nil {
					t.Errorf("ValidatePath: unexpected error: %v", validateErr)
				}
				if clearErr != nil {
					t.Errorf("ClearByPath: unexpected error: %v", clearErr)
				}
				return
			}
			if validateErr == nil || !strings.Contains(validateErr.Error(), tc.wantErr) {
				t.Errorf("ValidatePath: error = %v, want one containing %q", validateErr, tc.wantErr)
			}
			if clearErr == nil {
				t.Errorf("ClearByPath: expected an error, as ValidatePath gave %v", validateErr)
			}
		})
	}
}
