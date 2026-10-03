package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// NewFunc builds a Tool from a typed function. Args must be a struct; its JSON
// Schema is derived from the struct's fields (see SchemaFor), and the model's
// arguments are decoded into it before fn runs. Missing required fields are
// reported back to the model as an error rather than reaching fn.
//
//	type WeatherArgs struct {
//		City  string `json:"city" desc:"City name, e.g. Paris"`
//		Units string `json:"units,omitempty" enum:"metric,imperial"`
//	}
//	weather, err := tools.NewFunc("get_weather", "Current weather for a city",
//		func(ctx context.Context, a WeatherArgs) (Weather, error) { ... })
func NewFunc[Args, Result any](name, description string, fn func(context.Context, Args) (Result, error)) (Tool, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	t := reflect.TypeOf((*Args)(nil)).Elem()
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("tool %q: argument type %s must be a struct", name, t)
	}
	schema, err := SchemaFor(t)
	if err != nil {
		return nil, fmt.Errorf("tool %q: %w", name, err)
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("tool %q: %w", name, err)
	}
	required, _ := schema["required"].([]string)
	return &funcTool[Args, Result]{name: name, description: description, schema: raw, required: required, fn: fn}, nil
}

// MustFunc is NewFunc that panics on error, for package-level tool variables.
func MustFunc[Args, Result any](name, description string, fn func(context.Context, Args) (Result, error)) Tool {
	t, err := NewFunc(name, description, fn)
	if err != nil {
		panic(err)
	}
	return t
}

type funcTool[Args, Result any] struct {
	name, description string
	schema            json.RawMessage
	required          []string
	fn                func(context.Context, Args) (Result, error)
}

func (f *funcTool[A, R]) Name() string                { return f.name }
func (f *funcTool[A, R]) Description() string         { return f.description }
func (f *funcTool[A, R]) Parameters() json.RawMessage { return f.schema }

func (f *funcTool[A, R]) Call(ctx context.Context, raw json.RawMessage) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(raw, &present); err != nil {
		return nil, fmt.Errorf("arguments must be a JSON object: %w", err)
	}
	var missing []string
	for _, k := range f.required {
		if _, ok := present[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required argument(s): %s", strings.Join(missing, ", "))
	}
	var args A
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return f.fn(ctx, args)
}

// SchemaFor derives a JSON Schema from a Go type. For struct fields it uses:
//   - the json tag for the property name ("-" skips the field);
//   - required unless the json tag has omitempty or the field is a pointer;
//   - a `desc:"..."` tag for the description;
//   - an `enum:"a,b,c"` tag for allowed string values.
//
// Embedded structs without a json name are flattened. The output sticks to the
// schema subset every provider accepts (no $ref, no additionalProperties).
func SchemaFor(t reflect.Type) (map[string]any, error) {
	return schemaFor(t, map[reflect.Type]bool{})
}

var rawMessageType = reflect.TypeOf(json.RawMessage(nil))

func schemaFor(t reflect.Type, visiting map[reflect.Type]bool) (map[string]any, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == rawMessageType {
		return map[string]any{}, nil
	}
	if t.PkgPath() == "time" && t.Name() == "Time" {
		return map[string]any{"type": "string", "format": "date-time"}, nil
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}, nil
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, nil
	case reflect.Interface:
		return map[string]any{}, nil
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "contentEncoding": "base64"}, nil
		}
		items, err := schemaFor(t.Elem(), visiting)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "array", "items": items}, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("map key type %s is not supported; use string keys", t.Key())
		}
		return map[string]any{"type": "object"}, nil
	case reflect.Struct:
		if visiting[t] {
			return nil, fmt.Errorf("recursive type %s is not supported", t)
		}
		visiting[t] = true
		defer delete(visiting, t)

		props := map[string]any{}
		var required []string
		if err := addFields(t, props, &required, visiting); err != nil {
			return nil, err
		}
		s := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			sort.Strings(required)
			s["required"] = required
		}
		return s, nil
	default:
		return nil, fmt.Errorf("type %s is not supported in tool arguments", t)
	}
}

func addFields(t reflect.Type, props map[string]any, required *[]string, visiting map[reflect.Type]bool) error {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				if err := addFields(ft, props, required, visiting); err != nil {
					return err
				}
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		fs, err := schemaFor(f.Type, visiting)
		if err != nil {
			return fmt.Errorf("field %s: %w", f.Name, err)
		}
		if d := f.Tag.Get("desc"); d != "" {
			fs["description"] = d
		}
		if e := f.Tag.Get("enum"); e != "" {
			fs["enum"] = strings.Split(e, ",")
		}
		props[name] = fs
		if !strings.Contains(opts, "omitempty") && f.Type.Kind() != reflect.Pointer {
			*required = append(*required, name)
		}
	}
	return nil
}
