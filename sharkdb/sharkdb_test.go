package sharkdb

import (
	"reflect"
	"testing"

	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/schema"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestCanalLoggingToggle(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			listener := NewBinlogListener(&BinlogConfig{
				Logger:         zap.New(core),
				EnableCanalLog: enabled,
			})

			listener.canalConfig().Logger.Info("canal test")
			if got, want := logs.Len(), map[bool]int{false: 0, true: 1}[enabled]; got != want {
				t.Fatalf("got %d canal logs; want %d", got, want)
			}
		})
	}
}

func TestOnUpdateDispatchesOnlyAfterRow(t *testing.T) {
	listener := NewBinlogListener(&BinlogConfig{Database: "testdb"})
	var gotTable string
	var gotData map[string]any
	listener.OnUpdate(func(table string, data map[string]any) {
		gotTable, gotData = table, data
	})

	event := &canal.RowsEvent{
		Table: &schema.Table{
			Schema: "testdb",
			Name:   "items",
			Columns: []schema.TableColumn{
				{Name: "id"},
				{Name: "name"},
			},
		},
		Action: canal.UpdateAction,
		Rows: [][]any{
			{int64(1), "old"},
			{int64(1), "new"},
		},
	}
	handler := &eventHandler{listener: listener}
	if err := handler.OnRow(event); err != nil {
		t.Fatal(err)
	}

	want := map[string]any{"id": int64(1), "name": "new"}
	if gotTable != "items" || !reflect.DeepEqual(gotData, want) {
		t.Fatalf("got table %q and data %#v; want items and %#v", gotTable, gotData, want)
	}
}
