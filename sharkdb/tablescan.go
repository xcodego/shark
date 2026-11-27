package sharkdb

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path"
	"reflect"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

// tableScanOrder 是一个排序字段及其方向。
type tableScanOrder struct {
	columns string // 列名（同时用于 SQL 和从游标记录取值，匹配规则见 getFieldValue）
	order   string // 排序方向："asc" 或 "desc"
}

// TableScan 是基于 Keyset 游标的双向分页与批量导出扫描器。
//
// 排序字段（Asc/Desc）会直接拼入 ORDER BY 和游标 WHERE，必须是后端固定的可信列名，
// 不能来自用户输入；多个排序字段合起来应能唯一确定一行（如末尾加主键），
// 并与数据库索引匹配，否则翻页会漏行或重复。
type TableScan[T any] struct {
	pagesize int              // 每页行数，默认 5000
	orders   []tableScanOrder // 排序字段列表（多字段联合排序）
}

// NewTableScan 创建表扫描器，默认每页 5000 行。需先通过 Asc/Desc 配置排序字段。
func NewTableScan[T any]() *TableScan[T] {
	return &TableScan[T]{
		pagesize: 5000,
	}
}

// PageSize 设置每页扫描行数，非正数使用默认值。
func (p *TableScan[T]) PageSize(size int) *TableScan[T] {
	p.pagesize = size
	if p.pagesize <= 0 {
		p.pagesize = 5000
	}
	return p
}

// Asc 追加升序排序字段，多次调用按顺序联合排序。字段必须是可信列名。
func (p *TableScan[T]) Asc(columns ...string) *TableScan[T] {
	for _, column := range columns {
		p.orders = append(p.orders, tableScanOrder{columns: column, order: "asc"})
	}
	return p
}

// Desc 追加降序排序字段，多次调用按顺序联合排序。字段必须是可信列名。
func (p *TableScan[T]) Desc(columns ...string) *TableScan[T] {
	for _, column := range columns {
		p.orders = append(p.orders, tableScanOrder{columns: column, order: "desc"})
	}
	return p
}

// getFieldValueFromStruct 递归（含嵌入结构体）查找 tagName 标签（"gorm" 的 column: 或 "json"）等于 name 的字段，
// 找到返回字段值，否则返回 nil。
func getFieldValueFromStruct(v reflect.Value, t reflect.Type, name string, tagName string) any {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		fv := v.Field(i)

		// 嵌入（匿名）结构体：递归搜索其内部字段
		if sf.Anonymous {
			// 嵌入指针需解引用，nil 则跳过
			if fv.Kind() == reflect.Ptr {
				if fv.IsNil() {
					continue
				}
				fv = fv.Elem()
			}
			if fv.Kind() == reflect.Struct {
				if result := getFieldValueFromStruct(fv, fv.Type(), name, tagName); result != nil {
					return result
				}
			}
			continue
		}

		// 按指定标签匹配
		switch tagName {
		case "gorm":
			gormTag := sf.Tag.Get("gorm")
			if gormTag == "" {
				continue
			}
			tags := strings.Split(gormTag, ";")
			for _, tag := range tags {
				if strings.HasPrefix(tag, "column:") {
					column := strings.TrimPrefix(tag, "column:")
					if column == name {
						if fv.IsValid() && fv.CanInterface() {
							return fv.Interface()
						}
					}
				}
			}
		case "json":
			jsonTag := sf.Tag.Get("json")
			if jsonTag == "" {
				continue
			}
			jsonName := strings.Split(jsonTag, ",")[0] // 取逗号前部分（忽略 omitempty 等选项）
			if jsonName == name {
				if fv.IsValid() && fv.CanInterface() {
					return fv.Interface()
				}
			}
		}
	}
	return nil
}

// getFieldValue 按 name 从模型对象取游标值；name 含 "." 时只取最后一段（去掉表名前缀）。
//
// 匹配优先级：struct 字段名（含嵌入字段）> gorm column tag > json tag。
// 均未找到返回 nil，此时游标条件会以 NULL 比较，导致翻页结果为空或不正确，
// 因此排序字段必须能在 T 中按上述规则找到。
func getFieldValue[T any](obj *T, name string) any {
	if obj == nil {
		return nil
	}
	if len(name) == 0 {
		return nil
	}
	sp := strings.Split(name, ".")
	name = sp[len(sp)-1]
	v := reflect.ValueOf(obj)
	if v.Kind() == reflect.Ptr {
		v = v.Elem() // 解引用指针
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	// 先按 struct 字段名查找（FieldByName 已支持嵌入字段）
	field := v.FieldByName(name)
	if field.IsValid() && field.CanInterface() {
		return field.Interface()
	}
	// 再按 gorm column tag
	if result := getFieldValueFromStruct(v, t, name, "gorm"); result != nil {
		return result
	}
	// 最后按 json tag
	if result := getFieldValueFromStruct(v, t, name, "json"); result != nil {
		return result
	}
	return nil
}

// Next 获取游标 last 之后的下一页数据，顺序由 Asc/Desc 定义。
//
// db 为已应用 WHERE 条件的查询（内部使用新 Session，不污染原 db）；
// last 为上一页最后一条记录，首次查询传 nil。未配置排序字段时不加 ORDER BY 和游标条件。
//
// 生成的 SQL（以 Asc("create_time", "id") 为例）：
//
//	SELECT * FROM table
//	WHERE (create_time > ?) OR (create_time = ? AND id > ?)
//	ORDER BY create_time ASC, id ASC
//	LIMIT pageSize
//
// 使用示例：
//
//	scan := sharkdb.NewTableScan[User]().
//	    PageSize(200).
//	    Asc("create_time", "id")
//
//	var last *User
//	for {
//	    results, err := scan.Next(db.Where("deleted = 0"), last)
//	    if err != nil {
//	        log.Printf("扫描出错: %v", err)
//	        break
//	    }
//	    if len(results) == 0 {
//	        break // 扫描完毕
//	    }
//	    last = &results[len(results)-1] // 游标指向本页最后一条
//
//	    // 处理当前页数据
//	    for _, u := range results {
//	        fmt.Printf("ID=%d, Name=%s, Created=%v\n", u.Id, u.Name, u.CreateTime)
//	    }
//	}
func (p *TableScan[T]) Next(db *gorm.DB, last *T) ([]T, error) {
	tx := db.Session(&gorm.Session{})
	for _, order := range p.orders {
		tx = tx.Order(order.columns + " " + order.order)
	}
	// Keyset 条件：(a > ?) OR (a = ? AND b > ?) ...，逐字段展开以支持联合排序
	if last != nil && len(p.orders) > 0 {
		var orSQL []string
		var args []any
		for i := 0; i < len(p.orders); i++ {
			var andSQL []string
			// 前置字段取等
			for j := 0; j < i; j++ {
				col := p.orders[j]
				andSQL = append(andSQL,
					fmt.Sprintf("%s = ?", col.columns),
				)
				args = append(args,
					getFieldValue(last, col.columns),
				)
			}
			// 当前字段按方向比较
			col := p.orders[i]
			op := ">"
			if strings.ToLower(col.order) == "desc" {
				op = "<"
			}
			andSQL = append(andSQL,
				fmt.Sprintf("%s %s ?", col.columns, op),
			)
			args = append(args,
				getFieldValue(last, col.columns),
			)
			orSQL = append(orSQL,
				"("+strings.Join(andSQL, " AND ")+")",
			)
		}
		tx = tx.Where(
			strings.Join(orSQL, " OR "),
			args...,
		)
	}
	var list []T
	err := tx.Limit(p.pagesize).Find(&list).Error
	return list, err
}

// Prev 获取游标 first 之前的上一页数据，返回顺序与 Next 一致。
//
// db 为已应用 WHERE 条件的查询；first 为当前页第一条记录。
// 实现：反转 ORDER BY 和 Keyset 比较方向，查询后再反转切片。
// 同一 scan 实例可交替调用 Next 和 Prev。
//
// 使用示例：
//
//	// 当前页是 results
//	if len(results) > 0 {
//	    first := &results[0] // 当前页第一条作为游标
//	    prevResults, err := scan.Prev(db.Where("deleted = 0"), first)
//	    if err != nil {
//	        log.Printf("获取上一页失败: %v", err)
//	    }
//	    // prevResults 的顺序与 Next 一致
//	    for _, u := range prevResults {
//	        fmt.Println(u.Id)
//	    }
//	}
func (p *TableScan[T]) Prev(db *gorm.DB, first *T) ([]T, error) {
	// 反转 ORDER BY
	tx := db.Session(&gorm.Session{})
	for _, order := range p.orders {
		orderType := strings.ToLower(order.order)
		if orderType == "asc" {
			orderType = "desc"
		} else {
			orderType = "asc"
		}
		tx = tx.Order(order.columns + " " + orderType)
	}
	// 反向 Keyset 条件，结构同 Next，比较方向相反
	if first != nil && len(p.orders) > 0 {
		var orSQL []string
		var args []any
		for i := 0; i < len(p.orders); i++ {
			var andSQL []string
			for j := 0; j < i; j++ {
				col := p.orders[j]
				andSQL = append(andSQL,
					fmt.Sprintf("%s = ?", col.columns),
				)
				args = append(args,
					getFieldValue(first, col.columns),
				)
			}
			col := p.orders[i]
			op := "<"
			if strings.ToLower(col.order) == "desc" {
				op = ">"
			}
			andSQL = append(andSQL,
				fmt.Sprintf("%s %s ?", col.columns, op),
			)
			args = append(args,
				getFieldValue(first, col.columns),
			)
			orSQL = append(orSQL,
				"("+strings.Join(andSQL, " AND ")+")",
			)
		}
		tx = tx.Where(
			strings.Join(orSQL, " OR "),
			args...,
		)
	}
	var list []T
	err := tx.Limit(p.pagesize).Find(&list).Error
	if err != nil {
		return nil, err
	}
	// 恢复为与 Next 一致的顺序
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list, nil
}

// ExportExcel 将查询结果分页导出为 Excel 文件（.xlsx）。
//
// 使用 Next 逐页读取并通过 StreamWriter 流式写入，内存只占一页数据，适合大数据量。
// 必须先配置排序字段（Asc/Desc），否则返回错误；排序字段须为可信列名。
//
// 参数：
//   - ctx:    取消导出
//   - db:     已应用 WHERE 条件的查询
//   - name:   文件名前缀，自动追加时间戳，如 "用户列表_20250618120000.xlsx"
//   - header: 表头
//   - cb:     行转换函数，返回值顺序与 header 一致；每个值通过 fmt.Sprint 转为文本单元格
//
// 返回生成的文件名（不含目录），文件位于 os.TempDir()。
//
// 使用示例：
//
//	type User struct {
//	    Id         int64     `gorm:"column:id"`
//	    Name       string    `gorm:"column:name"`
//	    Phone      string    `gorm:"column:phone"`
//	    CreateTime time.Time `gorm:"column:create_time"`
//	}
//
//	scan := sharkdb.NewTableScan[User]().
//	    PageSize(500).
//	    Asc("create_time", "id")
//
//	filePath, err := scan.ExportExcel(
//	    context.Background(),
//	    db.Where("status = 1"),
//	    "活跃用户列表",
//	    []any{"ID", "姓名", "手机号", "创建时间"},
//	    func(u User) []any {
//	        return []any{
//	            u.Id,
//	            u.Name,
//	            u.Phone,
//	            u.CreateTime.Format("2006-01-02 15:04:05"),
//	        }
//	    },
//	)
//	if err != nil {
//	    log.Fatalf("导出失败: %v", err)
//	}
//	fmt.Println("文件已生成:", path.Join(os.TempDir(), filePath))
func (p *TableScan[T]) ExportExcel(ctx context.Context, db *gorm.DB, name string, header []any, cb func(T) []any) (string, error) {
	// 导出必须有稳定排序；否则每次 LIMIT 可能读取同一页，导致导出无法结束。
	if len(p.orders) == 0 {
		return "", fmt.Errorf("tablescan export requires at least one sort order")
	}
	excelFile := excelize.NewFile()
	defer excelFile.Close()

	// 流式写入，避免全量数据进内存
	streamWriter, err := excelFile.NewStreamWriter("Sheet1")
	if err != nil {
		return "", err
	}

	// 表头
	if err := streamWriter.SetRow("A1", header); err != nil {
		return "", err
	}

	// 逐页扫描并写入
	index := 0
	var last *T
	for {
		// 响应取消
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		values, err := p.Next(db, last)
		if err != nil {
			return "", err
		}
		if len(values) == 0 {
			break
		}
		for i := 0; i < len(values); i++ {
			row := cb(values[i])
			d := make([]any, 0, len(row))
			for _, v := range row {
				d = append(d, excelize.Cell{StyleID: 49, Value: fmt.Sprint(v)})
			}
			// 第 1 行是表头，数据从第 2 行开始
			cell, _ := excelize.CoordinatesToCellName(1, index+2)
			if err := streamWriter.SetRow(cell, d); err != nil {
				return "", err
			}
			index++
		}
		last = &values[len(values)-1]
	}

	if err := streamWriter.Flush(); err != nil {
		return "", err
	}

	fileName := fmt.Sprintf("%v_%v.xlsx", name, time.Now().Format("20060102150405"))
	if err := excelFile.SaveAs(path.Join(os.TempDir(), fileName)); err != nil {
		return "", err
	}
	return fileName, nil
}

// ExportCsv 将查询结果分页导出为 CSV 文件（.csv）。
//
// 使用 Next 逐页读取并通过 encoding/csv 流式写入，内存只占一页数据，适合大数据量。
// 必须先配置排序字段（Asc/Desc），否则返回错误；排序字段须为可信列名。
// 参数含义同 ExportExcel；返回生成的文件名（不含目录），文件位于 os.TempDir()。
//
// 使用示例：
//
//	filePath, err := scan.ExportCsv(
//	    context.Background(),
//	    db.Where("status = 1"),
//	    "活跃用户列表",
//	    []any{"ID", "姓名", "手机号", "创建时间"},
//	    func(u User) []any {
//	        return []any{
//	            u.Id,
//	            u.Name,
//	            u.Phone,
//	            u.CreateTime.Format("2006-01-02 15:04:05"),
//	        }
//	    },
//	)
func (p *TableScan[T]) ExportCsv(ctx context.Context, db *gorm.DB, name string, header []any, cb func(T) []any) (string, error) {
	// 导出必须有稳定排序；否则每次 LIMIT 可能读取同一页，导致导出无法结束。
	if len(p.orders) == 0 {
		return "", fmt.Errorf("tablescan export requires at least one sort order")
	}
	fileName := fmt.Sprintf("%v_%v.csv", name, time.Now().Format("20060102150405"))
	file, err := os.Create(path.Join(os.TempDir(), fileName))
	if err != nil {
		return "", err
	}
	defer file.Close()

	csvWriter := csv.NewWriter(file)

	headerRow := make([]string, 0, len(header))
	for _, h := range header {
		headerRow = append(headerRow, fmt.Sprint(h))
	}
	if err := csvWriter.Write(headerRow); err != nil {
		return "", err
	}

	// 逐页扫描并写入
	var last *T
	for {
		// 响应取消
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		values, err := p.Next(db, last)
		if err != nil {
			return "", err
		}
		if len(values) == 0 {
			break
		}
		for i := 0; i < len(values); i++ {
			row := cb(values[i])
			rowStr := make([]string, 0, len(row))
			for _, v := range row {
				rowStr = append(rowStr, fmt.Sprint(v))
			}
			if err := csvWriter.Write(rowStr); err != nil {
				return "", err
			}
		}
		last = &values[len(values)-1]
	}

	csvWriter.Flush()
	if err := csvWriter.Error(); err != nil {
		return "", err
	}

	return fileName, nil
}
