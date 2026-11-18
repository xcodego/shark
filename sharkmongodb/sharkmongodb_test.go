package sharkmongodb

import (
	"reflect"
	"testing"
)

func TestDispatchUpdate(t *testing.T) {
	for _, operation := range []string{"update", "replace"} {
		t.Run(operation, func(t *testing.T) {
			listener := NewBinlogListener(nil)
			var gotTable string
			var gotData map[string]any
			listener.OnUpdate(func(table string, data map[string]any) {
				gotTable, gotData = table, data
			})

			event := &changeEvent{
				OperationType:            operation,
				FullDocument:             map[string]any{"_id": 1, "value": "new"},
				FullDocumentBeforeChange: map[string]any{"_id": 1, "value": "old"},
			}
			event.NS.Coll = "items"
			if err := listener.dispatch(event); err != nil {
				t.Fatal(err)
			}
			if gotTable != "items" || !reflect.DeepEqual(gotData, event.FullDocument) {
				t.Fatalf("got table %q and data %#v; want items and %#v", gotTable, gotData, event.FullDocument)
			}
		})
	}
}

func TestDispatchUpdateWithoutFullDocument(t *testing.T) {
	listener := NewBinlogListener(nil)
	called := false
	listener.OnUpdate(func(string, map[string]any) { called = true })
	event := &changeEvent{OperationType: "update", DocumentKey: map[string]any{"_id": 1}}
	event.NS.Coll = "items"
	if err := listener.dispatch(event); err == nil {
		t.Fatal("expected an error for a missing full document")
	}
	if called {
		t.Fatal("update callback called without a full document")
	}
}
