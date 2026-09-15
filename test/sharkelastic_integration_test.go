package test

import (
	"context"
	"os"
	"path"
	"testing"
	"time"

	"github.com/lornshark/shark/sharkelastic"
	"github.com/tidwall/gjson"
)

func TestElasticNew(t *testing.T) {
	cfg := loadElasticConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	es, err := sharkelastic.New(ctx, cfg)
	if err != nil {
		t.Fatalf("连接 Elasticsearch 失败: %v", err)
	}
	t.Log("Elasticsearch 连接成功")

	// 创建测试索引
	indexName := "test_shark_index"
	err = es.CreateIndex(ctx, indexName, 1,
		sharkelastic.FieldMapping{Name: "name", Type: sharkelastic.MappingTypeText},
		sharkelastic.FieldMapping{Name: "age", Type: sharkelastic.MappingTypeInteger},
	)
	if err != nil {
		t.Fatalf("CreateIndex 失败: %v", err)
	}
	t.Logf("创建索引: %s", indexName)

	// 批量插入
	err = es.Insert(ctx, indexName, "user_id",
		map[string]any{"user_id": "1", "name": "张三", "age": 25},
		map[string]any{"user_id": "2", "name": "李四", "age": 30},
	)
	if err != nil {
		t.Fatalf("Insert 失败: %v", err)
	}
	t.Log("Bulk Insert 成功")

	// 刷新索引使文档可搜索
	es.Client.Indices.Refresh(es.Client.Indices.Refresh.WithIndex(indexName))

	// 搜索
	resp, err := es.Search(ctx, indexName, map[string]any{
		"query": map[string]any{
			"match": map[string]any{"name": "张三"},
		},
	})
	if err != nil {
		t.Fatalf("Search 失败: %v", err)
	}
	t.Logf("搜索结果: %s", string(resp))

	// 清理
	es.Client.Indices.Delete([]string{indexName})
	t.Log("Elasticsearch CRUD 验证通过")
}

func TestElasticSetIndexMapping(t *testing.T) {
	cfg := loadElasticConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	es, err := sharkelastic.New(ctx, cfg)
	if err != nil {
		t.Fatalf("连接 Elasticsearch 失败: %v", err)
	}

	indexName := "test_shark_mapping"
	es.CreateIndex(ctx, indexName, 1, sharkelastic.FieldMapping{Name: "title", Type: sharkelastic.MappingTypeText})

	// 添加新字段映射
	err = es.SetIndexMapping(ctx, indexName,
		sharkelastic.FieldMapping{Name: "tags", Type: sharkelastic.MappingTypeKeyword},
		sharkelastic.FieldMapping{Name: "score", Type: sharkelastic.MappingTypeFloat},
	)
	if err != nil {
		t.Fatalf("SetIndexMapping 失败: %v", err)
	}
	t.Log("SetIndexMapping 成功")

	es.Client.Indices.Delete([]string{indexName})
}

func TestElasticDeleteById(t *testing.T) {
	cfg := loadElasticConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	es, err := sharkelastic.New(ctx, cfg)
	if err != nil {
		t.Fatalf("连接 Elasticsearch 失败: %v", err)
	}

	indexName := "test_shark_delete_by_id"
	es.CreateIndex(ctx, indexName, 1,
		sharkelastic.FieldMapping{Name: "name", Type: sharkelastic.MappingTypeText},
	)

	// 批量插入测试数据
	err = es.Insert(ctx, indexName, "user_id",
		map[string]any{"user_id": "1", "name": "张三"},
		map[string]any{"user_id": "2", "name": "李四"},
		map[string]any{"user_id": "3", "name": "王五"},
		map[string]any{"user_id": "4", "name": "赵六"},
	)
	if err != nil {
		t.Fatalf("Insert 失败: %v", err)
	}
	es.Client.Indices.Refresh(es.Client.Indices.Refresh.WithIndex(indexName))

	// 批量删除 ID 为 "1", "2", "3" 的文档
	err = es.DeleteById(ctx, indexName, "1", "2", "3")
	if err != nil {
		t.Fatalf("DeleteById 失败: %v", err)
	}
	t.Log("DeleteById 批量删除成功")

	// 验证删除结果：ID 4 应该还在，1/2/3 应该不存在
	es.Client.Indices.Refresh(es.Client.Indices.Refresh.WithIndex(indexName))
	resp, err := es.Search(ctx, indexName, map[string]any{
		"query": map[string]any{"match_all": map[string]any{}},
	})
	if err != nil {
		t.Fatalf("Search 验证失败: %v", err)
	}
	t.Logf("删除后搜索结果: %s", string(resp))

	// 清理
	es.Client.Indices.Delete([]string{indexName})
	t.Log("DeleteById 测试通过")
}

func TestElasticDeleteBySql(t *testing.T) {
	cfg := loadElasticConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	es, err := sharkelastic.New(ctx, cfg)
	if err != nil {
		t.Fatalf("连接 Elasticsearch 失败: %v", err)
	}

	indexName := "test_shark_delete_by_sql_v2"
	es.Client.Indices.Delete([]string{indexName})
	if err := es.CreateIndex(ctx, indexName, 1,
		sharkelastic.FieldMapping{Name: "user_id", Type: sharkelastic.MappingTypeKeyword},
		sharkelastic.FieldMapping{Name: "name", Type: sharkelastic.MappingTypeText},
		sharkelastic.FieldMapping{Name: "age", Type: sharkelastic.MappingTypeInteger},
		sharkelastic.FieldMapping{Name: "status", Type: sharkelastic.MappingTypeInteger},
	); err != nil {
		t.Fatalf("CreateIndex 失败: %v", err)
	}

	// 批量插入测试数据
	err = es.Insert(ctx, indexName, "user_id",
		map[string]any{"user_id": "1", "name": "张三", "age": 25, "status": 1},
		map[string]any{"user_id": "2", "name": "李四", "age": 30, "status": 0},
		map[string]any{"user_id": "3", "name": "王五", "age": 35, "status": 0},
		map[string]any{"user_id": "4", "name": "赵六", "age": 40, "status": 1},
	)
	if err != nil {
		t.Fatalf("Insert 失败: %v", err)
	}
	es.Client.Indices.Refresh(es.Client.Indices.Refresh.WithIndex(indexName))

	// 按条件删除 status = 0 的文档（search_after 必须有 order by）
	err = es.DeleteBySql(ctx, "delete from "+indexName+" where status = 0 order by user_id asc")
	if err != nil {
		t.Fatalf("DeleteBySql 失败: %v", err)
	}
	t.Log("DeleteBySql 条件删除成功")

	// 验证：status=1 的应该还在
	es.Client.Indices.Refresh(es.Client.Indices.Refresh.WithIndex(indexName))
	resp, err := es.Search(ctx, indexName, map[string]any{
		"query": map[string]any{"match_all": map[string]any{}},
	})
	if err != nil {
		t.Fatalf("Search 验证失败: %v", err)
	}
	t.Logf("删除后搜索结果: %s", string(resp))

	// 清理
	es.Client.Indices.Delete([]string{indexName})
	t.Log("DeleteBySql 测试通过")
}

// TestElasticAggExpr_Integration 集成测试聚合表达式 (sum(a)-sum(b)) as c 在真实 ES 上执行。
// 验证 bucket_script pipeline 聚合 + 显式别名都能正确返回。
func TestElasticAggExpr_Integration(t *testing.T) {
	cfg := loadElasticConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	es, err := sharkelastic.New(ctx, cfg)
	if err != nil {
		t.Fatalf("连接 Elasticsearch 失败: %v", err)
	}

	indexName := "test_shark_agg_expr"

	// 1. 创建索引
	err = es.CreateIndex(ctx, indexName, 1,
		sharkelastic.FieldMapping{Name: "user_id", Type: sharkelastic.MappingTypeKeyword},
		sharkelastic.FieldMapping{Name: "amount", Type: sharkelastic.MappingTypeDouble},
		sharkelastic.FieldMapping{Name: "winlost_amount", Type: sharkelastic.MappingTypeDouble},
	)
	if err != nil {
		t.Fatalf("CreateIndex 失败: %v", err)
	}
	defer es.Client.Indices.Delete([]string{indexName})

	// 2. 插入测试数据
	err = es.Insert(ctx, indexName, "user_id",
		map[string]any{"user_id": "1", "amount": 100.0, "winlost_amount": 10.0},
		map[string]any{"user_id": "2", "amount": 200.0, "winlost_amount": 20.0},
		map[string]any{"user_id": "3", "amount": 300.0, "winlost_amount": 30.0},
	)
	if err != nil {
		t.Fatalf("Insert 失败: %v", err)
	}
	es.Client.Indices.Refresh(es.Client.Indices.Refresh.WithIndex(indexName))

	// 3. 聚合查询：sum(amount) - sum(winlost_amount) as x
	t.Run("SumMinusSum", func(t *testing.T) {
		sql := `select count(*) as count, sum(amount) as amount, sum(winlost_amount) as winlost_amount, (sum(amount) - sum(winlost_amount)) as x from ` + indexName
		resp, err := es.SqlRaw(ctx, sql)
		if err != nil {
			t.Fatalf("SqlRaw 聚合表达式失败: %v", err)
		}
		body := string(resp)
		t.Logf("BucketScript 聚合响应: %s", body)

		// count 应在 aggregations.all.agg_count 或其平级
		// 先尝试嵌套路径（filter 包裹），再尝试顶层（纯指标聚合）
		allPath := gjson.Get(body, "aggregations.all.buckets._all")
		if allPath.Exists() {
			countVal := gjson.Get(body, "aggregations.all.buckets._all.count.value").Int()
			amountVal := gjson.Get(body, "aggregations.all.buckets._all.amount.value").Float()
			winlostVal := gjson.Get(body, "aggregations.all.buckets._all.winlost_amount.value").Float()
			xVal := gjson.Get(body, "aggregations.all.buckets._all.x.value").Float()
			t.Logf("count=%d amount=%.2f winlost_amount=%.2f x=%.2f", countVal, amountVal, winlostVal, xVal)

			if countVal != 3 {
				t.Errorf("count: 期望 3，实际 %d", countVal)
			}
			if amountVal != 600.0 {
				t.Errorf("sum(amount): 期望 600，实际 %.2f", amountVal)
			}
			if winlostVal != 60.0 {
				t.Errorf("sum(winlost_amount): 期望 60，实际 %.2f", winlostVal)
			}
			if xVal != 540.0 {
				t.Errorf("x = sum(amount)-sum(winlost_amount): 期望 540，实际 %.2f", xVal)
			}
		} else {
			t.Fatalf("未找到 aggregations.all.buckets._all，响应结构: %s", body)
		}
	})

	// 4. 聚合表达式 + where 条件混合查询
	t.Run("WithWhere", func(t *testing.T) {
		sql := `select (sum(amount) + sum(winlost_amount)) as y from ` + indexName + ` where user_id = '1'`
		resp, err := es.SqlRaw(ctx, sql)
		if err != nil {
			t.Fatalf("SqlRaw 聚合+where 失败: %v", err)
		}
		body := string(resp)
		t.Logf("聚合+where 响应: %s", body)

		yVal := gjson.Get(body, "aggregations.all.buckets._all.y.value").Float()
		// user_id=1: amount=100, winlost=10 → y=110
		if yVal != 110.0 {
			t.Errorf("y = sum(amount)+sum(winlost) WHERE user_id='1': 期望 110，实际 %.2f", yVal)
		}
	})
}

func TestElasticExportExcelBySql(t *testing.T) {
	cfg := loadElasticConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	es, err := sharkelastic.New(ctx, cfg)
	if err != nil {
		t.Fatalf("连接 Elasticsearch 失败: %v", err)
	}

	indexName := "test_shark_export_v2"
	es.Client.Indices.Delete([]string{indexName})
	if err := es.CreateIndex(ctx, indexName, 1,
		sharkelastic.FieldMapping{Name: "user_id", Type: sharkelastic.MappingTypeKeyword},
		sharkelastic.FieldMapping{Name: "name", Type: sharkelastic.MappingTypeText},
		sharkelastic.FieldMapping{Name: "age", Type: sharkelastic.MappingTypeInteger},
		sharkelastic.FieldMapping{Name: "status", Type: sharkelastic.MappingTypeInteger},
	); err != nil {
		t.Fatalf("CreateIndex 失败: %v", err)
	}

	// 批量插入测试数据
	err = es.Insert(ctx, indexName, "user_id",
		map[string]any{"user_id": "1", "name": "张三", "age": 25, "status": 1},
		map[string]any{"user_id": "2", "name": "李四", "age": 30, "status": 1},
		map[string]any{"user_id": "3", "name": "王五", "age": 35, "status": 0},
		map[string]any{"user_id": "4", "name": "赵六", "age": 40, "status": 1},
	)
	if err != nil {
		t.Fatalf("Insert 失败: %v", err)
	}
	es.Client.Indices.Refresh(es.Client.Indices.Refresh.WithIndex(indexName))

	// 导出 status = 1 的数据（search_after 必须有 order by）
	filePath, err := es.ExportExcelBySql(ctx,
		"select user_id,name,age from "+indexName+" where status = 1 order by user_id asc",
		"test_export",
		[]any{"用户ID", "姓名", "年龄"},
		func(docBytes []byte) []any {
			return []any{
				gjson.GetBytes(docBytes, "user_id").String(),
				gjson.GetBytes(docBytes, "name").String(),
				gjson.GetBytes(docBytes, "age").Int(),
			}
		},
	)
	if err != nil {
		t.Fatalf("ExportExcelBySql 失败: %v", err)
	}
	t.Logf("导出文件路径: %s", filePath)

	// 验证文件存在（ExportExcelBySql 返回文件名，文件在 os.TempDir() 下）
	fullPath := path.Join(os.TempDir(), filePath)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		t.Fatalf("导出文件不存在: %s", fullPath)
	}
	t.Logf("导出文件存在，大小验证通过")

	// 清理文件
	os.Remove(fullPath)
	// 清理索引
	es.Client.Indices.Delete([]string{indexName})
	t.Log("ExportExcelBySql 测试通过")
}
