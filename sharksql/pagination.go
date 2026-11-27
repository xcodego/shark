package sharksql

import (
	"github.com/xcodego/shark/sharkerror"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PageQuery 分页查询，返回当前页数据和总数。
// page 小于 1 按 1 处理，pageSize 小于 10 按 10 处理；
// 为防止深分页，page*pageSize 超过 10000 时返回错误。
func PageQuery[T any](db *gorm.DB, page int, pageSize int) ([]T, int64, *sharkerror.Error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 10 {
		pageSize = 10
	}
	if page*pageSize > 10000 {
		return nil, 0, sharkerror.New(1, "offset cannot exceed 10000")
	}
	// 复制 Session 计数，并移除 ORDER BY，避免排序影响 COUNT 且不污染 db
	var total int64
	countdb := db.Session(&gorm.Session{Initialized: true})
	delete(countdb.Statement.Clauses, clause.OrderBy{}.Name())
	err := countdb.Count(&total).Error
	if err != nil {
		return nil, 0, sharkerror.New(1, err.Error())
	}
	offset := (page - 1) * pageSize
	var results []T
	err = db.Offset(offset).Limit(pageSize).Find(&results).Error
	if err != nil {
		return nil, 0, sharkerror.New(1, err.Error())
	}
	return results, total, nil
}
