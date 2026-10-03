package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

type Address struct {
	City string `json:"city"`
}

type Base struct {
	TraceID string `json:"trace_id,omitempty"`
}

type searchArgs struct {
	Base
	Query    string            `json:"query" desc:"What to search for"`
	Limit    int               `json:"limit,omitempty"`
	Sort     string            `json:"sort,omitempty" enum:"relevance,date"`
	Tags     []string          `json:"tags,omitempty"`
	Since    *time.Time        `json:"since"`
	Where    Address           `json:"where"`
	Labels   map[string]string `json:"labels,omitempty"`
	Ignored  string            `json:"-"`
	internal string
}

func TestSchemaFor(t *testing.T) {
	s, err := SchemaFor(reflect.TypeOf(searchArgs{}))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(s)
	want := `{"properties":{"labels":{"type":"object"},"limit":{"type":"integer"},"query":{"description":"What to search for","type":"string"},"since":{"format":"date-time","type":"string"},"sort":{"enum":["relevance","date"],"type":"string"},"tags":{"items":{"type":"string"},"type":"array"},"trace_id":{"type":"string"},"where":{"properties":{"city":{"type":"string"}},"required":["city"],"type":"object"}},"required":["query","where"],"type":"object"}`
	if string(got) != want {
		t.Fatalf("schema mismatch\n got: %s\nwant: %s", got, want)
	}
}

type node struct {
	Next *node `json:"next"`
}

func TestSchemaForRejectsUnsupportedTypes(t *testing.T) {
	if _, err := SchemaFor(reflect.TypeOf(node{})); err == nil || !strings.Contains(err.Error(), "recursive") {
		t.Fatalf("expected recursive type error, got %v", err)
	}
	type badMap struct {
		M map[int]string `json:"m"`
	}
	if _, err := SchemaFor(reflect.TypeOf(badMap{})); err == nil {
		t.Fatal("expected error for non-string map keys")
	}
	type badChan struct {
		C chan int `json:"c"`
	}
	if _, err := SchemaFor(reflect.TypeOf(badChan{})); err == nil {
		t.Fatal("expected error for channels")
	}
}

type addArgs struct {
	A int `json:"a"`
	B int `json:"b"`
}

func TestNewFuncCall(t *testing.T) {
	add := MustFunc("add", "Add two integers", func(_ context.Context, a addArgs) (int, error) {
		return a.A + a.B, nil
	})
	if add.Name() != "add" || !strings.Contains(string(add.Parameters()), `"required":["a","b"]`) {
		t.Fatalf("unexpected tool: %s %s", add.Name(), add.Parameters())
	}

	got, err := add.Call(context.Background(), json.RawMessage(`{"a":2,"b":3}`))
	if err != nil || got != 5 {
		t.Fatalf("Call = %v, %v", got, err)
	}
	if _, err := add.Call(context.Background(), json.RawMessage(`{"a":2}`)); err == nil || !strings.Contains(err.Error(), "b") {
		t.Fatalf("expected missing-argument error naming b, got %v", err)
	}
	if _, err := add.Call(context.Background(), json.RawMessage(`[1,2]`)); err == nil {
		t.Fatal("expected error for non-object arguments")
	}
	if _, err := add.Call(context.Background(), json.RawMessage(`{"a":"x","b":1}`)); err == nil {
		t.Fatal("expected error for wrongly typed argument")
	}
}

func TestNewFuncValidation(t *testing.T) {
	if _, err := NewFunc("bad name", "", func(context.Context, addArgs) (int, error) { return 0, nil }); err == nil {
		t.Fatal("expected invalid name error")
	}
	if _, err := NewFunc("scalar", "", func(context.Context, int) (int, error) { return 0, nil }); err == nil {
		t.Fatal("expected error for non-struct arguments")
	}
}

func TestNoArgTool(t *testing.T) {
	now := MustFunc("now", "Current time", func(context.Context, struct{}) (string, error) { return "noon", nil })
	if string(now.Parameters()) != `{"properties":{},"type":"object"}` {
		t.Fatalf("schema = %s", now.Parameters())
	}
	if got, err := now.Call(context.Background(), nil); err != nil || got != "noon" {
		t.Fatalf("Call = %v, %v", got, err)
	}
}

func TestRegistry(t *testing.T) {
	a := MustFunc("a", "", func(context.Context, struct{}) (int, error) { return 1, nil })
	b := MustFunc("b", "", func(context.Context, struct{}) (int, error) { return 2, nil })
	r, err := NewRegistry(b, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Register(a); err == nil {
		t.Fatal("expected duplicate registration error")
	}
	if all := r.All(); len(all) != 2 || all[0].Name() != "a" {
		t.Fatalf("All() should be sorted: %v", all)
	}
	sel, err := r.Select([]string{"b"})
	if err != nil || len(sel) != 1 || sel[0].Name() != "b" {
		t.Fatalf("Select = %v, %v", sel, err)
	}
	if _, err := r.Select([]string{"nope"}); err == nil {
		t.Fatal("expected unknown tool error")
	}
}

func TestCallContext(t *testing.T) {
	ctx := WithCallContext(context.Background(), "s1", "n1")
	if SessionID(ctx) != "s1" || NodeID(ctx) != "n1" {
		t.Fatalf("got %q %q", SessionID(ctx), NodeID(ctx))
	}
	if SessionID(context.Background()) != "" {
		t.Fatal("empty context should have no session")
	}
}
