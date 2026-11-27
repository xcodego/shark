package sharkelastic

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/elastic/go-elasticsearch/v9"
	"github.com/elastic/go-elasticsearch/v9/esapi"
	"github.com/tidwall/gjson"
	"github.com/xuri/excelize/v2"
)

// Client 封装 Elasticsearch 客户端及索引、查询辅助方法。请使用 New 创建。
type Client struct {
	Elastic *elasticsearch.Client
}

// MappingType 是 Elasticsearch 字段类型。
type MappingType string

// 常用的 Elasticsearch 字段类型。
const (
	MappingTypeText    MappingType = "text"
	MappingTypeKeyword MappingType = "keyword"
	MappingTypeInteger MappingType = "integer"
	MappingTypeLong    MappingType = "long"
	MappingTypeFloat   MappingType = "float"
	MappingTypeDouble  MappingType = "double"
	MappingTypeBoolean MappingType = "boolean"
	MappingTypeDate    MappingType = "date"
	MappingTypeBinary  MappingType = "binary"
	MappingTypeObject  MappingType = "object"
	MappingTypeNested  MappingType = "nested"
)

// FieldMapping 描述索引中的一个字段映射。
type FieldMapping struct {
	Name   string
	Type   MappingType
	Format string // 可选，字段格式，例如 date 类型的 "yyyy-MM-dd" 或 "yyyy-MM-dd HH:mm:ss"
}

// CreateIndex 创建索引，可指定分片数和字段映射。
// shards <= 0 时使用 ES 默认值；不传 mappings 则只创建索引，由 ES 动态推断字段。
//
// 使用示例:
//
//	err := client.CreateIndex(ctx, "users", 2,
//	    sharkelastic.FieldMapping{Name: "name", Type: sharkelastic.MappingTypeText},
//	    sharkelastic.FieldMapping{Name: "age", Type: sharkelastic.MappingTypeInteger},
//	    sharkelastic.FieldMapping{Name: "email", Type: sharkelastic.MappingTypeKeyword},
//	)
//
//	// 不指定映射
//	err = client.CreateIndex(ctx, "orders", 1)
func (s *Client) CreateIndex(ctx context.Context, index string, shards int, mappings ...FieldMapping) error {
	if index == "" {
		return fmt.Errorf("索引名称不能为空")
	}

	settings := map[string]any{}
	if shards > 0 {
		settings["number_of_shards"] = shards
	}

	body := map[string]any{}
	if len(settings) > 0 {
		body["settings"] = settings
	}
	if len(mappings) > 0 {
		props := map[string]any{}
		for _, m := range mappings {
			field := map[string]any{"type": string(m.Type)}
			if m.Format != "" {
				field["format"] = m.Format
			}
			props[m.Name] = field
		}
		body["mappings"] = map[string]any{"properties": props}
	}

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyBytes, err := sonic.Marshal(body)
		if err != nil {
			return fmt.Errorf("序列化创建索引请求体失败: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req := esapi.IndicesCreateRequest{
		Index: index,
		Body:  bodyReader,
	}
	resp, err := req.Do(ctx, s.Elastic)
	if err != nil {
		return fmt.Errorf("创建索引请求执行失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.IsError() {
		errBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("创建索引失败(状态:%s): %s", resp.Status(), string(errBytes))
	}

	return nil
}

// CreateIndexStrict 创建严格模式（dynamic: strict）的索引，写入未在 mappings 中定义的字段会被 ES 拒绝。
// shards <= 0 时使用 ES 默认值。
//
// 使用示例:
//
//	err := client.CreateIndexStrict(ctx, "users", 2,
//	    sharkelastic.FieldMapping{Name: "name", Type: sharkelastic.MappingTypeText},
//	    sharkelastic.FieldMapping{Name: "age", Type: sharkelastic.MappingTypeInteger},
//	)
func (s *Client) CreateIndexStrict(ctx context.Context, index string, shards int, mappings ...FieldMapping) error {
	if index == "" {
		return fmt.Errorf("索引名称不能为空")
	}

	settings := map[string]any{}
	if shards > 0 {
		settings["number_of_shards"] = shards
	}

	body := map[string]any{}
	if len(settings) > 0 {
		body["settings"] = settings
	}

	mappingsBody := map[string]any{
		"dynamic": "strict",
	}
	if len(mappings) > 0 {
		props := map[string]any{}
		for _, m := range mappings {
			field := map[string]any{"type": string(m.Type)}
			if m.Format != "" {
				field["format"] = m.Format
			}
			props[m.Name] = field
		}
		mappingsBody["properties"] = props
	}
	body["mappings"] = mappingsBody

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyBytes, err := sonic.Marshal(body)
		if err != nil {
			return fmt.Errorf("序列化创建索引请求体失败: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req := esapi.IndicesCreateRequest{
		Index: index,
		Body:  bodyReader,
	}
	resp, err := req.Do(ctx, s.Elastic)
	if err != nil {
		return fmt.Errorf("创建索引请求执行失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.IsError() {
		errBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("创建索引失败(状态:%s): %s", resp.Status(), string(errBytes))
	}

	return nil
}

// SetIndexMapping 为已存在的索引添加字段映射（ES 不允许修改已有字段的类型），至少需要一个 mapping。
//
// 使用示例:
//
//	err := client.SetIndexMapping(ctx, "users",
//	    sharkelastic.FieldMapping{Name: "phone", Type: sharkelastic.MappingTypeKeyword},
//	    sharkelastic.FieldMapping{Name: "birthday", Type: sharkelastic.MappingTypeDate},
//	)
func (s *Client) SetIndexMapping(ctx context.Context, index string, mappings ...FieldMapping) error {
	if index == "" {
		return fmt.Errorf("索引名称不能为空")
	}
	if len(mappings) == 0 {
		return fmt.Errorf("至少需要提供一个字段映射")
	}

	props := map[string]any{}
	for _, m := range mappings {
		field := map[string]any{"type": string(m.Type)}
		if m.Format != "" {
			field["format"] = m.Format
		}
		props[m.Name] = field
	}
	body := map[string]any{
		"properties": props,
	}
	bodyBytes, err := sonic.Marshal(body)
	if err != nil {
		return fmt.Errorf("序列化映射请求体失败: %w", err)
	}

	req := esapi.IndicesPutMappingRequest{
		Index: []string{index},
		Body:  bytes.NewReader(bodyBytes),
	}
	resp, err := req.Do(ctx, s.Elastic)
	if err != nil {
		return fmt.Errorf("更新映射请求执行失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.IsError() {
		errBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("更新映射失败(状态:%s): %s", resp.Status(), string(errBytes))
	}

	return nil
}

// Search 在指定索引中执行搜索，query 为非空的 Elasticsearch 查询 DSL，返回原始响应字节。
//
// 使用示例:
//
//	searchParams := map[string]any{
//	    "query": map[string]any{
//	        "match": map[string]any{
//	            "name": "张三",
//	        },
//	    },
//	}
//	respBytes, err := elasticClient.Search(context.Background(), "users", searchParams)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Println("搜索结果:", string(respBytes))
func (s *Client) Search(ctx context.Context, index string, query map[string]any) ([]byte, error) {
	if s.Elastic == nil {
		return nil, fmt.Errorf("Elasticsearch 客户端未初始化")
	}
	if index == "" {
		return nil, fmt.Errorf("索引名称不能为空")
	}
	if len(query) == 0 {
		return nil, fmt.Errorf("搜索参数不能为空")
	}

	queryBytes, err := sonic.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("搜索参数序列化失败: %w", err)
	}

	searchReq := esapi.SearchRequest{
		Index: []string{index},
		Body:  bytes.NewReader(queryBytes),
	}

	resp, err := searchReq.Do(ctx, s.Elastic)
	if err != nil {
		return nil, fmt.Errorf("搜索请求执行失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.IsError() {
		errBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("搜索失败(状态:%s), 读取错误响应失败: %w", resp.Status(), readErr)
		}
		return nil, fmt.Errorf("搜索失败: %s", string(errBytes))
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取搜索响应失败: %w", err)
	}

	return respBytes, nil
}

// upsertBatchSize 是 Insert/Upsert 每个 Bulk 请求包含的文档数，兼顾吞吐与请求体大小。
const upsertBatchSize = 1000

// bulkResponse 是 Bulk API 响应中用于判错的子集。
type bulkResponse struct {
	Errors bool `json:"errors"`
	Items  []map[string]struct {
		Status int `json:"status"`
		Error  struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
		} `json:"error"`
	} `json:"items"`
}

// sendBulkRequest 发送单批 Bulk 请求。Bulk 即使部分失败 HTTP 状态也是 200，所以还要检查 errors 字段并汇总每条失败原因。
func (s *Client) sendBulkRequest(ctx context.Context, buf *bytes.Buffer) error {
	if s.Elastic == nil {
		return fmt.Errorf("Elasticsearch 客户端未初始化")
	}

	bulkReq := esapi.BulkRequest{
		Body: bytes.NewReader(buf.Bytes()),
	}
	resp, err := bulkReq.Do(ctx, s.Elastic)
	if err != nil {
		return fmt.Errorf("Bulk 请求执行失败: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取 Bulk 响应失败: %w", err)
	}
	if resp.IsError() {
		return fmt.Errorf("Bulk 请求失败(状态:%s): %s", resp.Status(), string(respBytes))
	}

	var br bulkResponse
	if err := sonic.Unmarshal(respBytes, &br); err != nil {
		return fmt.Errorf("解析 Bulk 响应失败: %w", err)
	}

	if br.Errors {
		var errMsgs []string
		for i, item := range br.Items {
			for _, result := range item {
				if result.Error.Type != "" || result.Error.Reason != "" {
					errMsgs = append(errMsgs, fmt.Sprintf(
						"第 %d 个文档: [%d] %s: %s", i+1, result.Status, result.Error.Type, result.Error.Reason,
					))
				}
			}
		}
		return fmt.Errorf("Bulk 操作部分失败:\n%s", strings.Join(errMsgs, "\n"))
	}

	return nil
}

// Insert 使用 Bulk API 批量写入文档（index 动作：_id 已存在时整体覆盖），内部按 1000 条一批自动分批。
// idField 是文档中作为 _id 的字段名，必须存在且非空。任意一批失败即返回错误，之前的批次不回滚。
//
// 使用示例:
//
//	docs := []any{
//	    map[string]any{"user_id": "1", "name": "张三", "age": 25},
//	    map[string]any{"user_id": "2", "name": "李四", "age": 30},
//	}
//	if err := client.Insert(ctx, "users", "user_id", docs...); err != nil {
//	    log.Fatal(err)
//	}
func (s *Client) Insert(ctx context.Context, index string, idField string, docs ...any) error {
	if index == "" {
		return fmt.Errorf("索引名称不能为空")
	}
	if idField == "" {
		return fmt.Errorf("文档Id字段名称不能为空")
	}
	if len(docs) == 0 {
		return fmt.Errorf("至少需要提供一个文档")
	}

	for start := 0; start < len(docs); start += upsertBatchSize {
		end := start + upsertBatchSize
		if end > len(docs) {
			end = len(docs)
		}
		batch := docs[start:end]

		// Bulk 请求体为 NDJSON：每个文档先写 action 行，再写数据行
		var buf bytes.Buffer
		for i, doc := range batch {
			docBytes, err := sonic.Marshal(doc)
			if err != nil {
				return fmt.Errorf("第 %d 个文档序列化失败: %w", start+i+1, err)
			}

			idResult := gjson.GetBytes(docBytes, idField)
			if !idResult.Exists() {
				return fmt.Errorf("第 %d 个文档中未找到ID字段 '%s'", start+i+1, idField)
			}
			docID := idResult.String()
			if docID == "" {
				return fmt.Errorf("第 %d 个文档的ID字段 '%s' 值为空", start+i+1, idField)
			}

			action := map[string]any{
				"index": map[string]any{
					"_index": index,
					"_id":    docID,
				},
			}
			actionBytes, err := sonic.Marshal(action)
			if err != nil {
				return fmt.Errorf("第 %d 个文档 action 序列化失败: %w", start+i+1, err)
			}
			buf.Write(actionBytes)
			buf.WriteByte('\n')

			buf.Write(docBytes)
			buf.WriteByte('\n')
		}

		if err := s.sendBulkRequest(ctx, &buf); err != nil {
			return fmt.Errorf("第 %d-%d 批插入失败: %w", start+1, end, err)
		}
	}

	return nil
}

// Upsert 使用 Bulk API 批量 upsert 文档（不存在则插入，存在则局部更新），内部按 1000 条一批自动分批。
// idField 是文档中作为 _id 的字段名，必须存在且非空。任意一批失败即返回错误，之前的批次不回滚。
//
// 使用示例:
//
//	docs := []any{
//	    map[string]any{"user_id": "1", "name": "张三"},
//	    map[string]any{"user_id": "2", "name": "李四"},
//	}
//	if err := client.Upsert(ctx, "users", "user_id", docs...); err != nil {
//	    log.Fatal(err)
//	}
func (s *Client) Upsert(ctx context.Context, index string, idField string, docs ...any) error {
	if index == "" {
		return fmt.Errorf("索引名称不能为空")
	}
	if idField == "" {
		return fmt.Errorf("文档Id字段名称不能为空")
	}
	if len(docs) == 0 {
		return fmt.Errorf("至少需要提供一个文档")
	}

	for start := 0; start < len(docs); start += upsertBatchSize {
		end := start + upsertBatchSize
		if end > len(docs) {
			end = len(docs)
		}
		batch := docs[start:end]

		// Bulk 请求体为 NDJSON：每个文档先写 action 行，再写数据行
		var buf bytes.Buffer
		for i, doc := range batch {
			docBytes, err := sonic.Marshal(doc)
			if err != nil {
				return fmt.Errorf("第 %d 个文档序列化失败: %w", start+i+1, err)
			}

			idResult := gjson.GetBytes(docBytes, idField)
			if !idResult.Exists() {
				return fmt.Errorf("第 %d 个文档中未找到ID字段 '%s'", start+i+1, idField)
			}
			docID := idResult.String()
			if docID == "" {
				return fmt.Errorf("第 %d 个文档的ID字段 '%s' 值为空", start+i+1, idField)
			}

			action := map[string]any{
				"update": map[string]any{
					"_index": index,
					"_id":    docID,
				},
			}
			actionBytes, err := sonic.Marshal(action)
			if err != nil {
				return fmt.Errorf("第 %d 个文档 action 序列化失败: %w", start+i+1, err)
			}
			buf.Write(actionBytes)
			buf.WriteByte('\n')

			updateBody := map[string]any{"doc": doc, "doc_as_upsert": true}
			updateBytes, err := sonic.Marshal(updateBody)
			if err != nil {
				return fmt.Errorf("第 %d 个文档 update body 序列化失败: %w", start+i+1, err)
			}
			buf.Write(updateBytes)
			buf.WriteByte('\n')
		}

		if err := s.sendBulkRequest(ctx, &buf); err != nil {
			return fmt.Errorf("第 %d-%d 批 upsert 失败: %w", start+1, end, err)
		}
	}

	return nil
}

// DeleteById 使用 Bulk API 批量删除指定 ID 的文档，ids 支持字符串和各类数字（统一转为字符串 _id）。
// 任意一条删除失败则返回汇总错误；文档不存在按 ES 返回的 not_found 状态处理，不计为错误。
func (s *Client) DeleteById(ctx context.Context, index string, ids ...any) error {
	if index == "" {
		return fmt.Errorf("索引名称不能为空")
	}
	if len(ids) == 0 {
		return fmt.Errorf("至少需要提供一个文档ID")
	}

	var buf bytes.Buffer
	for i, id := range ids {
		var idStr string
		switch v := id.(type) {
		case string:
			idStr = v
		case int:
			idStr = fmt.Sprintf("%d", v)
		case int64:
			idStr = fmt.Sprintf("%d", v)
		case int32:
			idStr = fmt.Sprintf("%d", v)
		case uint:
			idStr = fmt.Sprintf("%d", v)
		case uint64:
			idStr = fmt.Sprintf("%d", v)
		case uint32:
			idStr = fmt.Sprintf("%d", v)
		case float64:
			idStr = fmt.Sprintf("%v", v)
		default:
			idStr = fmt.Sprintf("%v", id)
		}
		if idStr == "" {
			return fmt.Errorf("第 %d 个文档ID为空", i+1)
		}

		action := map[string]any{
			"delete": map[string]any{
				"_index": index,
				"_id":    idStr,
			},
		}
		actionBytes, err := sonic.Marshal(action)
		if err != nil {
			return fmt.Errorf("第 %d 个文档 action 序列化失败: %w", i+1, err)
		}
		buf.Write(actionBytes)
		buf.WriteByte('\n')
	}

	if err := s.sendBulkRequest(ctx, &buf); err != nil {
		return err
	}

	return nil
}

// batchSize 是 DeleteBySql 与导出函数每次 search_after 查询的文档数。
const batchSize = 1000

// DeleteBySql 按 SQL WHERE 条件分批删除文档，用 search_after 游标深分页，适合百万级数据。
//
// SQL 必须包含 order by，且排序字段组合必须唯一，否则游标会导致漏删或重复删除。
// 循环流程：search_after 查询（禁用 _source）→ 按 _id Bulk 删除 → 直到无结果。
//
// SQL 格式：delete from indexname where conditions order by field1 [asc|desc], ...
// where 语法与 Find/SqlRaw 一致。
//
// 使用示例：
//
//	err := client.DeleteBySql(ctx, "delete from users where status = 0 order by user_id asc")
func (s *Client) DeleteBySql(ctx context.Context, sql string) error {
	if strings.TrimSpace(sql) == "" {
		return fmt.Errorf("SQL 不能为空")
	}

	input := strings.TrimSpace(sql)
	lower := strings.ToLower(input)
	if !strings.HasPrefix(lower, "delete ") && !strings.HasPrefix(lower, "delete\t") {
		return fmt.Errorf("SQL 必须以 delete 开头，例如: delete from indexname where conditions order by id asc")
	}
	afterDelete := strings.TrimSpace(input[6:])

	pq, err := Build(afterDelete)
	if err != nil {
		return fmt.Errorf("解析 SQL 失败: %w", err)
	}
	if pq.Index == "" {
		return fmt.Errorf("SQL 缺少 from 子句指定索引")
	}

	queryClause, ok := pq.Body["query"]
	if !ok {
		queryClause = map[string]any{"match_all": map[string]any{}}
	}

	// search_after 以排序值作为游标，必须有 order by
	sortSpec, ok := pq.Body["sort"]
	if !ok {
		return fmt.Errorf("DeleteBySql 使用 search_after 游标，SQL 必须包含 order by 且排序字段必须唯一，例如: delete from users where status = 0 order by user_id asc")
	}

	var searchAfter []any
	for {
		searchBody := map[string]any{
			"query":   queryClause,
			"size":    batchSize,
			"sort":    sortSpec,
			"_source": false,
		}
		if len(searchAfter) > 0 {
			searchBody["search_after"] = searchAfter
		}

		respBytes, err := s.Search(ctx, pq.Index, searchBody)
		if err != nil {
			return fmt.Errorf("查询待删除文档失败: %w", err)
		}

		hits := gjson.GetBytes(respBytes, "hits.hits")
		if !hits.Exists() || !hits.IsArray() || len(hits.Array()) == 0 {
			break
		}

		var ids []any
		hitArr := hits.Array()
		for _, hit := range hitArr {
			id := hit.Get("_id")
			if !id.Exists() || id.String() == "" {
				return fmt.Errorf("查询结果中缺少 _id 字段")
			}
			ids = append(ids, id.String())
		}

		if err := s.DeleteById(ctx, pq.Index, ids...); err != nil {
			return fmt.Errorf("分批删除失败: %w", err)
		}

		if len(hitArr) < batchSize {
			break
		}

		// 以最后一条的 sort 值作为下一轮 search_after 游标
		searchAfter = nil
		lastSort := hitArr[len(hitArr)-1].Get("sort")
		if lastSort.Exists() && lastSort.IsArray() {
			for _, v := range lastSort.Array() {
				searchAfter = append(searchAfter, v.Value())
			}
		} else {
			break
		}
	}

	return nil
}

// ExportExcelBySql 按 SQL 条件用 search_after 游标分批查询并导出为 Excel(.xlsx)，
// 通过 excelize StreamWriter 流式写入，内存中始终只有一批数据，适合百万级数据。
//
// SQL 必须包含 from 和 order by，且排序字段组合必须唯一，否则游标会导致数据遗漏或重复。
// SQL 格式：select field,... from indexname where conditions order by field1 [asc|desc], ...
//
// 参数：
//   - ctx:    上下文，取消后导出中止并返回 ctx.Err()
//   - name:   文件名前缀，自动追加时间戳，文件写入 os.TempDir()
//   - header: Excel 表头
//   - cb:     行转换函数，入参为单个文档的 _source JSON，返回各列的值
//
// 返回生成的文件名（不含目录，位于 os.TempDir() 下）。
//
// 使用示例：
//
//	filePath, err := client.ExportExcelBySql(
//	    ctx,
//	    "select user_id,name,age from users where status = 1 order by user_id asc",
//	    "用户列表",
//	    []any{"用户ID", "姓名", "年龄"},
//	    func(docBytes []byte) []any {
//	        return []any{
//	            gjson.GetBytes(docBytes, "user_id").String(),
//	            gjson.GetBytes(docBytes, "name").String(),
//	            gjson.GetBytes(docBytes, "age").Int(),
//	        }
//	    },
//	)
func (s *Client) ExportExcelBySql(ctx context.Context, sql string, name string, header []any, cb func([]byte) []any) (string, error) {
	if strings.TrimSpace(sql) == "" {
		return "", fmt.Errorf("SQL 不能为空")
	}

	pq, err := Build(sql)
	if err != nil {
		return "", fmt.Errorf("解析 SQL 失败: %w", err)
	}
	if pq.Index == "" {
		return "", fmt.Errorf("SQL 缺少 from 子句指定索引")
	}

	queryClause, ok := pq.Body["query"]
	if !ok {
		queryClause = map[string]any{"match_all": map[string]any{}}
	}

	// search_after 以排序值作为游标，必须有 order by
	sortSpec, ok := pq.Body["sort"]
	if !ok {
		return "", fmt.Errorf("ExportExcelBySql 使用 search_after 游标，SQL 必须包含 order by 且排序字段必须唯一，例如: select user_id,name from users where status = 1 order by user_id asc")
	}

	excelFile := excelize.NewFile()
	defer excelFile.Close()

	streamWriter, err := excelFile.NewStreamWriter("Sheet1")
	if err != nil {
		return "", err
	}
	if err := streamWriter.SetRow("A1", header); err != nil {
		return "", err
	}

	rowIndex := 0
	var searchAfter []any
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		searchBody := map[string]any{
			"query": queryClause,
			"size":  batchSize,
			"sort":  sortSpec,
		}
		if len(searchAfter) > 0 {
			searchBody["search_after"] = searchAfter
		}

		respBytes, err := s.Search(ctx, pq.Index, searchBody)
		if err != nil {
			return "", fmt.Errorf("查询待导出文档失败: %w", err)
		}

		hits := gjson.GetBytes(respBytes, "hits.hits")
		if !hits.Exists() || !hits.IsArray() || len(hits.Array()) == 0 {
			break
		}

		hitArr := hits.Array()

		// 用 json.RawMessage 保留每个 _source 的原始 JSON 交给 cb
		sourcesJSON := gjson.GetBytes(respBytes, "hits.hits.#._source")
		var docs []json.RawMessage
		if err := json.Unmarshal([]byte(sourcesJSON.Raw), &docs); err != nil {
			return "", fmt.Errorf("解析文档数据失败: %w", err)
		}

		for _, doc := range docs {
			row := cb(doc)
			d := make([]any, 0, len(row))
			for _, v := range row {
				// StyleID 49 为文本格式("@")，避免长数字被 Excel 转成科学计数法
				d = append(d, excelize.Cell{StyleID: 49, Value: fmt.Sprint(v)})
			}
			cell, _ := excelize.CoordinatesToCellName(1, rowIndex+2)
			if err := streamWriter.SetRow(cell, d); err != nil {
				return "", err
			}
			rowIndex++
		}

		if len(hitArr) < batchSize {
			break
		}

		// 以最后一条的 sort 值作为下一轮 search_after 游标
		searchAfter = nil
		lastSort := hitArr[len(hitArr)-1].Get("sort")
		if lastSort.Exists() && lastSort.IsArray() {
			for _, v := range lastSort.Array() {
				searchAfter = append(searchAfter, v.Value())
			}
		} else {
			break
		}
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

// ExportCsvBySql 按 SQL 条件用 search_after 游标分批查询并导出为 CSV(.csv)，
// 通过 encoding/csv 流式写入，内存中始终只有一批数据，适合百万级数据。
//
// SQL 必须包含 from 和 order by，且排序字段组合必须唯一，否则游标会导致数据遗漏或重复。
// SQL 格式：select field,... from indexname where conditions order by field1 [asc|desc], ...
//
// 参数：
//   - ctx:    上下文，取消后导出中止并返回 ctx.Err()
//   - name:   文件名前缀，自动追加时间戳，文件写入 os.TempDir()
//   - header: CSV 表头
//   - cb:     行转换函数，入参为单个文档的 _source JSON，返回各列的值
//
// 返回生成的文件名（不含目录，位于 os.TempDir() 下）。
//
// 使用示例：
//
//	filePath, err := client.ExportCsvBySql(
//	    ctx,
//	    "select user_id,name,age from users where status = 1 order by user_id asc",
//	    "用户列表",
//	    []any{"用户ID", "姓名", "年龄"},
//	    func(docBytes []byte) []any {
//	        return []any{
//	            gjson.GetBytes(docBytes, "user_id").String(),
//	            gjson.GetBytes(docBytes, "name").String(),
//	            gjson.GetBytes(docBytes, "age").Int(),
//	        }
//	    },
//	)
func (s *Client) ExportCsvBySql(ctx context.Context, sql string, name string, header []any, cb func([]byte) []any) (string, error) {
	if strings.TrimSpace(sql) == "" {
		return "", fmt.Errorf("SQL 不能为空")
	}

	pq, err := Build(sql)
	if err != nil {
		return "", fmt.Errorf("解析 SQL 失败: %w", err)
	}
	if pq.Index == "" {
		return "", fmt.Errorf("SQL 缺少 from 子句指定索引")
	}

	queryClause, ok := pq.Body["query"]
	if !ok {
		queryClause = map[string]any{"match_all": map[string]any{}}
	}

	// search_after 以排序值作为游标，必须有 order by
	sortSpec, ok := pq.Body["sort"]
	if !ok {
		return "", fmt.Errorf("ExportCsvBySql 使用 search_after 游标，SQL 必须包含 order by 且排序字段必须唯一，例如: select user_id,name from users where status = 1 order by user_id asc")
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

	var searchAfter []any
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		searchBody := map[string]any{
			"query": queryClause,
			"size":  batchSize,
			"sort":  sortSpec,
		}
		if len(searchAfter) > 0 {
			searchBody["search_after"] = searchAfter
		}

		respBytes, err := s.Search(ctx, pq.Index, searchBody)
		if err != nil {
			return "", fmt.Errorf("查询待导出文档失败: %w", err)
		}

		hits := gjson.GetBytes(respBytes, "hits.hits")
		if !hits.Exists() || !hits.IsArray() || len(hits.Array()) == 0 {
			break
		}

		hitArr := hits.Array()

		// 用 json.RawMessage 保留每个 _source 的原始 JSON 交给 cb
		sourcesJSON := gjson.GetBytes(respBytes, "hits.hits.#._source")
		var docs []json.RawMessage
		if err := json.Unmarshal([]byte(sourcesJSON.Raw), &docs); err != nil {
			return "", fmt.Errorf("解析文档数据失败: %w", err)
		}

		for _, doc := range docs {
			row := cb(doc)
			rowStr := make([]string, 0, len(row))
			for _, v := range row {
				rowStr = append(rowStr, fmt.Sprint(v))
			}
			if err := csvWriter.Write(rowStr); err != nil {
				return "", err
			}
		}

		if len(hitArr) < batchSize {
			break
		}

		// 以最后一条的 sort 值作为下一轮 search_after 游标
		searchAfter = nil
		lastSort := hitArr[len(hitArr)-1].Get("sort")
		if lastSort.Exists() && lastSort.IsArray() {
			for _, v := range lastSort.Array() {
				searchAfter = append(searchAfter, v.Value())
			}
		} else {
			break
		}
	}

	csvWriter.Flush()
	if err := csvWriter.Error(); err != nil {
		return "", err
	}

	return fileName, nil
}

// Summary 执行聚合 SQL 查询，并将聚合值解码到 value。
// value 必须是结构体指针，字段的 json 标签需与 SQL 中的聚合别名一致。
// 聚合算术表达式由 Elasticsearch bucket_script 处理。
func (s *Client) Summary(ctx context.Context, sql string, value any) error {
	if strings.TrimSpace(sql) == "" {
		return fmt.Errorf("SQL 不能为空")
	}
	if value == nil {
		return fmt.Errorf("value 不能为 nil")
	}

	pq, err := Build(sql)
	if err != nil {
		return fmt.Errorf("构建查询失败: %w", err)
	}
	if pq.Index == "" {
		return fmt.Errorf("SQL 缺少 from 子句指定索引")
	}

	respBytes, err := s.Search(ctx, pq.Index, pq.Body)
	if err != nil {
		return err
	}

	// 聚合结果路径：含 bucket_script 时在 aggregations.all.buckets._all.<alias>.value，
	// 否则在 aggregations.<alias>.value
	basePath := "aggregations"
	if nested := gjson.GetBytes(respBytes, "aggregations.all.buckets._all"); nested.Exists() {
		basePath = "aggregations.all.buckets._all"
	}

	result := make(map[string]any)
	base := gjson.GetBytes(respBytes, basePath)
	base.ForEach(func(key, val gjson.Result) bool {
		v := val.Get("value")
		if v.Exists() {
			result[key.String()] = v.Value()
		}
		return true
	})

	if len(result) == 0 {
		return fmt.Errorf("ES 响应中未找到聚合结果值")
	}

	jsonBytes, err := sonic.Marshal(result)
	if err != nil {
		return fmt.Errorf("序列化聚合结果失败: %w", err)
	}
	if err := sonic.Unmarshal(jsonBytes, value); err != nil {
		return fmt.Errorf("反序列化聚合结果失败: %w", err)
	}

	return nil
}

// SqlRaw 解析 SQL 风格查询并返回 Elasticsearch 完整原始响应（含 hits.total、_score 等元数据）。
func (s *Client) SqlRaw(ctx context.Context, sql string) ([]byte, error) {
	if strings.TrimSpace(sql) == "" {
		return nil, fmt.Errorf("SQL 不能为空")
	}

	pq, err := Build(sql)
	if err != nil {
		return nil, err
	}
	if pq.Index == "" {
		return nil, fmt.Errorf("SQL 缺少 from 子句指定索引")
	}
	return s.Search(ctx, pq.Index, pq.Body)
}

// Find 执行 SQL 风格查询，提取 hits.hits[]._source 并反序列化到 result，面向"查文档列表"场景。
// 不返回 total、_score 等元数据，需要时用 SqlRaw 获取完整响应。
//
// SQL 格式：
//
//	[select field,...] from indexname where conditions [order by field [asc|desc], ...] [limit N] [offset N]
//
// 支持的运算符：= != > >= < <= in like and or ()。like 映射为 ES match 全文查询，不是 SQL 通配符。
// 不支持 BETWEEN、IS NULL、函数调用、子查询、JOIN；聚合请使用 Summary。
//
// 注意事项：
//   - result 必须是指向切片的指针（如 &[]User{}），结构体用 json tag 匹配文档字段名
//   - 无结果时 result 为空切片，不返回错误
//   - from 子句必填
//
// 使用示例：
//
//	type User struct {
//	    UserID string `json:"user_id"`
//	    Name   string `json:"name"`
//	    Age    int    `json:"age"`
//	}
//
//	var users []User
//	err := es.Find(ctx, "from users where status = 1", &users)
//	err = es.Find(ctx, "select name,age from users where age >= 18 limit 10 offset 5", &users)
func (s *Client) Find(ctx context.Context, sql string, result any) error {
	if strings.TrimSpace(sql) == "" {
		return fmt.Errorf("SQL 不能为空")
	}
	if result == nil {
		return fmt.Errorf("result 不能为 nil")
	}

	pq, err := Build(sql)
	if err != nil {
		return fmt.Errorf("构建查询失败: %w", err)
	}
	if pq.Index == "" {
		return fmt.Errorf("SQL 缺少 from 子句指定索引")
	}

	respBytes, err := s.Search(ctx, pq.Index, pq.Body)
	if err != nil {
		return err
	}

	sourcesJSON := gjson.GetBytes(respBytes, "hits.hits.#._source")
	if !sourcesJSON.Exists() {
		return fmt.Errorf("解析 ES 响应失败: 未找到 hits.hits")
	}

	if err := sonic.Unmarshal([]byte(sourcesJSON.Raw), result); err != nil {
		return fmt.Errorf("反序列化结果失败: %w", err)
	}

	return nil
}

// Page 执行分页 SQL 风格查询，将 _source 列表解码到 result 并返回命中总数。
// page 从 1 开始（<1 按 1），pageSize <1 按 10；会覆盖 SQL 中的 limit/offset。
// 总数取自 hits.total.value，ES 默认最多精确计数到 1 万。
func (s *Client) Page(ctx context.Context, sql string, page int, pageSize int, result any) (int64, error) {
	if strings.TrimSpace(sql) == "" {
		return 0, fmt.Errorf("SQL 不能为空")
	}
	if result == nil {
		return 0, fmt.Errorf("result 不能为 nil")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}

	pq, err := Build(sql)
	if err != nil {
		return 0, fmt.Errorf("构建查询失败: %w", err)
	}
	if pq.Index == "" {
		return 0, fmt.Errorf("SQL 缺少 from 子句指定索引")
	}

	pq.Body["size"] = pageSize
	pq.Body["from"] = (page - 1) * pageSize

	respBytes, err := s.Search(ctx, pq.Index, pq.Body)
	if err != nil {
		return 0, err
	}

	totalResult := gjson.GetBytes(respBytes, "hits.total.value")
	if !totalResult.Exists() {
		return 0, fmt.Errorf("解析 ES 响应失败: 未找到 hits.total.value")
	}
	total := totalResult.Int()

	sourcesJSON := gjson.GetBytes(respBytes, "hits.hits.#._source")
	if sourcesJSON.Exists() {
		if err := sonic.Unmarshal([]byte(sourcesJSON.Raw), result); err != nil {
			return 0, fmt.Errorf("反序列化结果失败: %w", err)
		}
	}

	return total, nil
}
