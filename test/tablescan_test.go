package test

import (
	"context"
	"strings"
	"testing"

	"github.com/xcodego/shark/sharkdb"
)

func TestTableScanExportRequiresSortOrder(t *testing.T) {
	scan := sharkdb.NewTableScan[struct{}]()

	if _, err := scan.ExportExcel(context.Background(), nil, "users", nil, nil); err == nil || !strings.Contains(err.Error(), "sort order") {
		t.Fatalf("ExportExcel error = %v, want missing-sort-order error", err)
	}
	if _, err := scan.ExportCsv(context.Background(), nil, "users", nil, nil); err == nil || !strings.Contains(err.Error(), "sort order") {
		t.Fatalf("ExportCsv error = %v, want missing-sort-order error", err)
	}
}
