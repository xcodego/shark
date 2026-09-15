package test

import (
	"testing"

	"github.com/lornshark/shark/sharkjson"
)

type testUser struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestParseJsonBytes(t *testing.T) {
	user := sharkjson.ParseJsonBytes[testUser]([]byte(`{"name":"Alice","age":30}`))
	if user == nil {
		t.Fatal("解析 JSON 不应返回 nil")
	}
	if user.Name != "Alice" || user.Age != 30 {
		t.Errorf("解析结果不匹配: %+v", user)
	}
}

func TestParseJsonBytesEmpty(t *testing.T) {
	user := sharkjson.ParseJsonBytes[testUser]([]byte{})
	if user != nil {
		t.Error("空字节应返回 nil")
	}
}

func TestParseJsonBytesInvalid(t *testing.T) {
	user := sharkjson.ParseJsonBytes[testUser]([]byte(`not json`))
	if user != nil {
		t.Error("非法 JSON 应返回 nil")
	}
}

func TestParseJsonString(t *testing.T) {
	user := sharkjson.ParseJsonString[testUser](`{"name":"Bob","age":25}`)
	if user == nil {
		t.Fatal("解析不应返回 nil")
	}
	if user.Name != "Bob" {
		t.Errorf("Name = %s, want Bob", user.Name)
	}
}

func TestParseJsonStringEmpty(t *testing.T) {
	user := sharkjson.ParseJsonString[testUser]("")
	if user != nil {
		t.Error("空字符串应返回 nil")
	}
}

func TestToJsonBytes(t *testing.T) {
	data := sharkjson.ToJsonBytes(map[string]int{"a": 1, "b": 2})
	if data == nil {
		t.Fatal("序列化不应返回 nil")
	}
	s := string(data)
	if s != `{"a":1,"b":2}` && s != `{"b":2,"a":1}` {
		t.Errorf("序列化结果: %s", s)
	}
}

func TestToJsonBytesNil(t *testing.T) {
	if sharkjson.ToJsonBytes(nil) != nil {
		t.Error("nil 输入应返回 nil")
	}
}

func TestToJsonString(t *testing.T) {
	s := sharkjson.ToJsonString([]string{"apple", "banana"})
	if s != `["apple","banana"]` {
		t.Errorf("ToJsonString = %s", s)
	}
}

func TestToJsonStringStruct(t *testing.T) {
	s := sharkjson.ToJsonString(testUser{Name: "Alice", Age: 30})
	if s != `{"name":"Alice","age":30}` {
		t.Errorf("ToJsonString struct = %s", s)
	}
}
