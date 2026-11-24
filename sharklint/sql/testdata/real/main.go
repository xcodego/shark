package real

import (
	"database/sql"

	"github.com/xcodego/shark/sharklint/sql/testdata/real/app/gconst"
	"github.com/xcodego/shark/sharksql"
	"gorm.io/gorm"
)

func queries(db *gorm.DB, raw *sql.DB, id int) {
	db.Table(gconst.Users).Where(sharksql.Eq(gconst.ID, id)).Find(nil)
	sqlText, args := sharksql.NewSql().Eq(gconst.ID, id).Build()
	db.Where(sqlText, args...)
	db.Table("users")
	db.Where("id = ?", id)
	db.Where(sharksql.Eq("id", id))
	raw.Query("SELECT * FROM users")
}

func createOrder(db *gorm.DB, id int) error {
	orderData := map[string]any{gconst.ID: id}
	return func(tx *gorm.DB) error {
		orderData[gconst.ID] = id + 1
		err := tx.Table(gconst.Users).Create(orderData).Error
		return err
	}(db)
}
