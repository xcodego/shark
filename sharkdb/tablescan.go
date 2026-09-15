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

// TableScan 提供基于 Keyset Pagination（游标分页）的高性能表扫描功能。
//
// 核心优势：
// 相比传统 LIMIT/OFFSET 分页（SELECT * FROM user LIMIT 100000, 20），
// TableScan 使用“上一条记录”作为游标进行分页：
//
//	SELECT * FROM user
//	WHERE id > ?
//	ORDER BY id ASC
//	LIMIT 20
//
// 优点：
//   - 深分页性能极高（不受 offset 影响）
//   - 不会随着页数增加变慢
//   - 可利用索引范围扫描
//   - 适合大表扫描、增量同步、数据导出、消息消费
//
// Keyset 游标 SQL 原理（以 Asc("create_time","id") 为例）：
//
// 下一页（Next）：
//
//	WHERE
//	(
//	    create_time > ?                          -- 第一排序字段大于游标值
//	)
//	OR
//	(
//	    create_time = ? AND id > ?              -- 第一字段相等时，比较第二字段
//	)
//	ORDER BY create_time ASC, id ASC
//
// 上一页（Prev，内部自动反转 ORDER BY 并 reverse 结果）：
//
//	WHERE
//	(
//	    create_time < ?
//	)
//	OR
//	(
//	    create_time = ? AND id < ?
//	)
//	ORDER BY create_time DESC, id DESC
//	（查询后反转切片，保证返回顺序与 Next 一致）
//
// 注意事项：
//  1. 排序字段必须建立联合索引（否则全表扫描）
//  2. 排序字段的值必须稳定（不可在扫描过程中被修改）
//  3. 最后一个排序字段最好唯一（如 id），避免漏数据
//  4. 不允许排序字段存在 NULL（NULL 无法比较）
//
// 推荐配置：
//
//	scan := NewTableScan[User]().
//	    PageSize(200).
//	    Asc("create_time", "id")
//
// 对应索引：
//
//	CREATE INDEX idx_ctime_id ON user(create_time, id);
//
// 典型使用示例（全表扫描导出）：
//
//	scan := sharkdb.NewTableScan[User]().
//	    PageSize(500).
//	    Asc("create_time", "id")
//
//	var last *User
//	for {
//	    results, err := scan.Next(db.Where("deleted = 0"), last)
//	    if err != nil {
//	        panic(err)
//	    }
//	    if len(results) == 0 {
//	        break // 扫描完毕
//	    }
//	    last = &results[len(results)-1] // 游标指向本页最后一条
//	    for _, u := range results {
//	        fmt.Println(u.Id, u.Name)
//	    }
//	}

// tableScanOrder 表示排序字段及方向。
// 未导出，仅供 TableScan 内部使用。
type tableScanOrder struct {
	columns string // 字段名（支持 struct 字段名 / gorm column tag / json tag）
	order   string // 排序方向："asc" 或 "desc"
}

// TableScan 是泛型表扫描器，支持游标分页（Keyset Pagination）双向翻页。
//
// 泛型参数 T 为表对应的 GORM 模型类型。
//
// 使用前必须配置：
//  1. PageSize：每页行数（建议 100~500）
//  2. Asc 或 Desc：排序字段（必须与数据库索引匹配）
//
// 零值 TableScan 不可直接使用，必须通过 NewTableScan() 创建并链式配置。
//
// 链式配置示例：
//
//	scan := sharkdb.NewTableScan[User]().
//	    PageSize(200).
//	    Asc("create_time").
//	    Asc("id")
type TableScan[T any] struct {
	pagesize int              // 默认每页 5000 行
	orders   []tableScanOrder // 排序字段列表（多字段联合排序）
}

// NewTableScan 创建一个泛型表扫描器。
// T 为 GORM 模型类型（如 User、Order 等）。
//
// 使用示例：
//
//	// 创建扫描器并链式配置
//	scan := sharkdb.NewTableScan[User]().
//	    PageSize(200).
//	    Asc("create_time", "id")
//
//	// 全表扫描
//	var last *User
//	for {
//	    results, err := scan.Next(db, last)
//	    if err != nil || len(results) == 0 {
//	        break
//	    }
//	    last = &results[len(results)-1]
//	    processBatch(results)
//	}
func NewTableScan[T any]() *TableScan[T] {
	return &TableScan[T]{
		pagesize: 5000, // 默认每页 5000 行
	}
}

// PageSize 设置每页扫描行数。
//
// 参数：
//   - size: 每页返回的最大行数。建议 100~500，太大会增加单次查询延迟，太小会增加查询次数
//
// 返回：*TableScan[T] 自身，支持链式调用
//
// 示例：
//
//	scan.PageSize(200)
//	scan.PageSize(500) // 可重复调用覆盖
func (p *TableScan[T]) PageSize(size int) *TableScan[T] {
	p.pagesize = size
	if p.pagesize <= 0 {
		p.pagesize = 5000
	}
	return p
}

// Asc 添加升序排序字段。支持多次调用或单次传入多字段。
//
// 字段名支持三种匹配方式（优先级从高到低）：
//  1. struct 字段名（如 "CreateTime"）
//  2. gorm column tag（如 gorm:"column:create_time" → "create_time"）
//  3. json tag（如 json:"create_time" → "create_time"）
//
// 参数：
//   - columns: 排序字段名（可变参数）
//
// 返回：*TableScan[T] 自身，支持链式调用
//
// 示例：
//
//	// 方式一：多次调用
//	scan.Asc("create_time").Asc("id")
//
//	// 方式二：单次多字段（等价）
//	scan.Asc("create_time", "id")
//
//	// 方式三：使用 gorm column tag 名
//	scan.Asc("create_time", "auto_id")
func (p *TableScan[T]) Asc(columns ...string) *TableScan[T] {
	for _, column := range columns {
		p.orders = append(p.orders, tableScanOrder{columns: column, order: "asc"})
	}
	return p
}

// Desc 添加降序排序字段。字段名匹配规则同 Asc。
//
// 参数：
//   - columns: 排序字段名（可变参数）
//
// 返回：*TableScan[T] 自身，支持链式调用
//
// 示例：
//
//	// 按分数降序 + ID 升序
//	scan.Desc("score").Asc("id")
//
//	// 单次多字段
//	scan.Desc("score", "create_time")
func (p *TableScan[T]) Desc(columns ...string) *TableScan[T] {
	for _, column := range columns {
		p.orders = append(p.orders, tableScanOrder{columns: column, order: "desc"})
	}
	return p
}

// getFieldValueFromStruct 递归搜索结构体（包含嵌入结构体）中匹配指定标签的字段值。
//
// 参数：
//   - v:    结构体的 reflect.Value
//   - t:    结构体的 reflect.Type
//   - name: 要匹配的字段名
//   - tagName: 标签类型（"gorm" 或 "json"），决定按哪种标签匹配
//
// 返回值：匹配成功时返回字段值，否则返回 nil。
func getFieldValueFromStruct(v reflect.Value, t reflect.Type, name string, tagName string) any {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		fv := v.Field(i)

		// 嵌入（匿名）结构体：递归搜索其内部字段
		if sf.Anonymous {
			// 处理嵌入指针类型（如 *gdbmodel.XGameOrder）
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

// getFieldValue 从模型对象中获取指定字段的值。
//
// 字段名匹配优先级（从高到低）：
//  1. struct 字段名（直接通过 FieldByName 匹配，支持嵌入字段）
//  2. gorm column tag（遍历所有字段的 gorm:"column:xxx" 标签，递归搜索嵌入结构体）
//  3. json tag（遍历所有字段的 json:"xxx" 标签，递归搜索嵌入结构体）
//
// 若三层匹配均未找到，返回 nil。
//
// 参数：
//   - obj:  模型对象指针
//   - name: 要获取的字段名（用于游标值比较）
func getFieldValue[T any](obj *T, name string) any {
	if obj == nil {
		return nil
	}
	v := reflect.ValueOf(obj)
	if v.Kind() == reflect.Ptr {
		v = v.Elem() // 解引用指针
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	// 第一层：直接按 struct 字段名查找（FieldByName 已支持递归搜索嵌入字段）
	field := v.FieldByName(name)
	if field.IsValid() && field.CanInterface() {
		return field.Interface()
	}
	// 第二层：按 gorm column tag 查找（递归搜索嵌入结构体）
	if result := getFieldValueFromStruct(v, t, name, "gorm"); result != nil {
		return result
	}
	// 第三层：按 json tag 查找（递归搜索嵌入结构体）
	if result := getFieldValueFromStruct(v, t, name, "json"); result != nil {
		return result
	}
	return nil
}

// Next 获取下一页数据（游标分页前向扫描）。
//
// 参数：
//   - db:   已应用 WHERE 条件的 GORM 查询（如 db.Where("deleted = 0")）。
//     内部会 New Session 避免污染原 db。
//   - last: 上一页的最后一条记录（游标）。首次查询传 nil。
//
// 返回值：
//   - []T:  当前页的数据切片（顺序由 Asc/Desc 定义）
//   - error: 查询失败时返回错误
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
	// 创建新 Session，避免修改原始 db 的查询条件
	tx := db.Session(&gorm.Session{})
	// 添加 ORDER BY
	for _, order := range p.orders {
		tx = tx.Order(order.columns + " " + order.order)
	}
	// 如果提供了游标，构建 Keyset 条件（多字段游标比较）
	if last != nil && len(p.orders) > 0 {
		var orSQL []string
		var args []any
		for i := 0; i < len(p.orders); i++ {
			var andSQL []string
			// 前置排序字段：值相等时继续比较下一个字段
			for j := 0; j < i; j++ {
				col := p.orders[j]
				andSQL = append(andSQL,
					fmt.Sprintf("%s = ?", col.columns),
				)
				args = append(args,
					getFieldValue(last, col.columns),
				)
			}
			// 当前排序字段：根据方向选择 > 或 <
			col := p.orders[i]
			op := ">"
			if strings.ToLower(col.order) == "desc" {
				op = "<" // 降序时游标比较方向反转
			}
			andSQL = append(andSQL,
				fmt.Sprintf("%s %s ?", col.columns, op),
			)
			args = append(args,
				getFieldValue(last, col.columns),
			)
			// 每组条件用 OR 连接
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

// Prev 获取上一页数据（游标分页后向扫描）。
//
// 参数：
//   - db:    已应用 WHERE 条件的 GORM 查询
//   - first: 当前页的第一条记录（游标）
//
// 返回值：
//   - []T:  上一页的数据切片（顺序与 Next 一致）
//   - error: 查询失败时返回错误
//
// 实现原理：
//  1. 反转所有 ORDER BY（ASC → DESC，DESC → ASC）
//  2. 反转 Keyset 比较方向（> ↔ <）
//  3. 查询后 reverse 切片，保证返回顺序与 Next() 一致
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
//
// 注意：
//   - Prev 查询比 Next 多一次切片 reverse 操作（O(n)），性能差异可忽略
//   - 同一 scan 实例可交替调用 Next 和 Prev
func (p *TableScan[T]) Prev(db *gorm.DB, first *T) ([]T, error) {
	// 创建新 Session 并反转 ORDER BY
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
	// 构建反向 Keyset 条件（比较方向反转：> → <）
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
			// 方向反转：前向扫描用 > 则后向用 <
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
	// 反转切片，保证返回顺序与 Next 一致
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list, nil
}

// ExportExcel 将全表数据导出为 Excel 文件（.xlsx）。
//
// 内部使用 TableScan.Next 逐页读取，通过 excelize 的 StreamWriter 流式写入，
// 内存占用始终只有一页数据量，适合百万级数据导出。
//
// 参数：
//   - ctx:    上下文，用于取消导出操作
//   - db:     已应用 WHERE 条件的 GORM 查询
//   - name:   导出文件名前缀（自动追加时间戳，如 "用户列表_20250618120000.xlsx"）
//   - header: Excel 表头（如 []any{"ID", "姓名", "手机号", "创建时间"}）
//   - cb:     行数据转换函数，入参为模型对象，出参为每列的值切片（顺序与 header 一致）
//
// 返回值：
//   - string: 生成的文件路径（位于系统临时目录，即 os.TempDir()）
//   - error:  导出失败时返回错误
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
//	fmt.Println("文件已生成:", filePath)
//
//	// 可选：移动文件到目标目录
//	os.Rename(filePath, "/desired/path/users.xlsx")
func (p *TableScan[T]) ExportExcel(ctx context.Context, db *gorm.DB, name string, header []any, cb func(T) []any) (string, error) {
	// 创建 Excel 文件
	excelFile := excelize.NewFile()
	defer excelFile.Close()

	// 创建流式写入器（StreamWriter），避免全量数据加载到内存
	streamWriter, err := excelFile.NewStreamWriter("Sheet1")
	if err != nil {
		return "", err
	}

	// 写入表头（A1 开始）
	if err := streamWriter.SetRow("A1", header); err != nil {
		return "", err
	}

	// 逐页扫描并流式写入
	index := 0
	var last *T
	for {
		// 上下文取消时提前退出
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		values, err := p.Next(db, last)
		if err != nil {
			return "", err
		}
		if len(values) == 0 {
			break // 扫描完毕
		}
		// 逐行转换并写入
		for i := 0; i < len(values); i++ {
			row := cb(values[i])
			d := make([]any, 0, len(row))
			for _, v := range row {
				d = append(d, excelize.Cell{StyleID: 49, Value: fmt.Sprint(v)})
			}
			// 计算单元格坐标：第 1 列，第 index+2 行（第 1 行是表头）
			cell, _ := excelize.CoordinatesToCellName(1, index+2)
			if err := streamWriter.SetRow(cell, d); err != nil {
				return "", err
			}
			index++
		}
		// 游标指向本页最后一条
		last = &values[len(values)-1]
	}

	// 刷新流式写入器，确保数据全部写入
	if err := streamWriter.Flush(); err != nil {
		return "", err
	}

	// 保存文件到临时目录
	fileName := fmt.Sprintf("%v_%v.xlsx", name, time.Now().Format("20060102150405"))
	if err := excelFile.SaveAs(path.Join(os.TempDir(), fileName)); err != nil {
		return "", err
	}
	return fileName, nil
}

// ExportCsv 将全表数据导出为 CSV 文件（.csv）。
//
// 内部使用 TableScan.Next 逐页读取，通过 encoding/csv 流式写入，
// 内存占用始终只有一页数据量，适合百万级数据导出。
//
// 参数：
//   - ctx:    上下文，用于取消导出操作
//   - db:     已应用 WHERE 条件的 GORM 查询
//   - name:   导出文件名前缀（自动追加时间戳，如 "用户列表_20250618120000.csv"）
//   - header: CSV 表头（如 []any{"ID", "姓名", "手机号", "创建时间"}）
//   - cb:     行数据转换函数，入参为模型对象，出参为每列的值切片（顺序与 header 一致）
//
// 返回值：
//   - string: 生成的文件路径（位于系统临时目录，即 os.TempDir()）
//   - error:  导出失败时返回错误
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
	fileName := fmt.Sprintf("%v_%v.csv", name, time.Now().Format("20060102150405"))
	file, err := os.Create(path.Join(os.TempDir(), fileName))
	if err != nil {
		return "", err
	}
	defer file.Close()

	csvWriter := csv.NewWriter(file)

	// 写入表头
	headerRow := make([]string, 0, len(header))
	for _, h := range header {
		headerRow = append(headerRow, fmt.Sprint(h))
	}
	if err := csvWriter.Write(headerRow); err != nil {
		return "", err
	}

	// 逐页扫描并流式写入
	var last *T
	for {
		// 上下文取消时提前退出
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		values, err := p.Next(db, last)
		if err != nil {
			return "", err
		}
		if len(values) == 0 {
			break // 扫描完毕
		}
		// 逐行转换并写入
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
		// 游标指向本页最后一条
		last = &values[len(values)-1]
	}

	// 刷新流式写入器，确保数据全部写入
	csvWriter.Flush()
	if err := csvWriter.Error(); err != nil {
		return "", err
	}

	return fileName, nil
}
