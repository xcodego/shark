package sharksql

import (
	"github.com/lornshark/shark/sharkerror"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PageQuery 执行泛型分页查询，返回指定页的数据和总记录数。
//
// 参数：
//   - db:       GORM 查询链（已包含 WHERE 条件等）
//   - page:     页码（从 1 开始，< 1 时自动修正为 1，> 500 时受 offset 限制）
//   - pageSize: 每页条数（< 10 时自动修正为 10）
//
// 返回值：
//   - []T:  当前页数据切片
//   - int64: 总记录数
//   - *sharkerror.Error: 查询失败时返回业务错误
//
// 限制：
//   - offset（page * pageSize）不能超过 10000，防止深分页性能问题
//   - 使用 Session(&gorm.Session{}) 创建独立会话执行 COUNT，避免被原查询链的 Select 覆盖
//
// 使用示例：
//
//	type User struct {
//	    ID     int64  `gorm:"column:id"`
//	    Name   string `gorm:"column:name"`
//	    Status int    `gorm:"column:status"`
//	}
//
//	// 分页查询状态正常的用户
//	db := gormDB.Where(sharksql.Eq("status", 1))
//	users, total, err := sharksql.PageQuery[User](db, 1, 20)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("第 1 页，共 %d 条记录\n", total)
//	for _, u := range users {
//	    fmt.Println(u.Name)
//	}
//
//	// 结合复杂条件分页
//	db = gormDB.Where(
//	    sharksql.Eq("status", 1),
//	    sharksql.Like("name", "张"),
//	    sharksql.Gte("age", 18),
//	).Order(sharksql.Desc("created_at"))
//	users, total, err = sharksql.PageQuery[User](db, 2, 10)
func PageQuery[T any](db *gorm.DB, page int, pageSize int) ([]T, int64, *sharkerror.Error) {
	// 参数修正
	if page < 1 {
		page = 1
	}
	if pageSize < 10 {
		pageSize = 10
	}
	// offset 限制：防止深分页拖垮数据库
	if page*pageSize > 10000 {
		return nil, 0, sharkerror.New(1, "offset cannot exceed 10000")
	}
	// 使用独立 Session 计数，避免被 Select 子句影响
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
