package real

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"gorm.io/gorm"
)

type rpcClient interface {
	Call(context.Context, ...grpc.CallOption) error
}

func transactionChecks(db *gorm.DB, client rpcClient, ctx context.Context) error {
	return db.Transaction(func(tx *gorm.DB) error {
		tx.Create(&struct{}{})
		_ = client.Call(ctx)
		time.Sleep(time.Second)
		return tx.Transaction(func(inner *gorm.DB) error {
			return inner.Create(&struct{}{}).Error
		})
	})
}

func transactionConnectionChecks(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var rows []struct{}
		query := tx.Model(&rows)
		if err := query.Find(&rows).Error; err != nil {
			return err
		}
		return db.Model(&rows).Find(&rows).Error
	})
}
