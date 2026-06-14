package internal

import "testing"

type clrInner struct {
	Secret string `json:"secret"`
	Keep   string `json:"keep"`
}

type clrOuter struct {
	Name   string              `json:"name"`
	Inner  clrInner            `json:"inner"`
	Items  []clrInner          `json:"items"`
	M      map[string]clrInner `json:"m"`
	Labels map[string]string   `json:"labels"`
	Dotted map[string]string   `json:"dotted"`
}

func newClrOuter() *clrOuter {
	return &clrOuter{
		Name:   "name",
		Inner:  clrInner{Secret: "s", Keep: "keep"},
		Items:  []clrInner{{Secret: "s0", Keep: "k0"}, {Secret: "s1", Keep: "k1"}},
		M:      map[string]clrInner{"x": {Secret: "sx", Keep: "kx"}},
		Labels: map[string]string{"secret": "v", "keep": "v"},
		Dotted: map[string]string{"a.b.c": "v"},
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
			name:    "unknown field errors",
			path:    "nope",
			wantErr: true,
		},
		{
			name:    "slice index out of range errors",
			path:    "items.5.secret",
			wantErr: true,
		},
		{
			name:    "navigate into scalar errors",
			path:    "name.foo",
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
