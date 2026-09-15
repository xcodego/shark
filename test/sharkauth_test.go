package test

import (
	"testing"

	"github.com/lornshark/shark/sharkauth"
)

func TestNormalizeAuthTree(t *testing.T) {
	fullTree := []*sharkauth.AuthNode{
		{
			Name: "系统管理",
			Children: []*sharkauth.AuthNode{
				{Name: "用户列表", Urls: []string{"/api/user/list"}},
				{Name: "用户详情", Urls: []string{"/api/user/detail"}},
				{Name: "角色管理", Children: []*sharkauth.AuthNode{
					{Name: "角色列表", Urls: []string{"/api/role/list"}},
				}},
			},
		},
	}

	// 创建默认无权限模板
	tree := sharkauth.NormalizeAuthTree(fullTree, 2)
	if len(tree) != 1 {
		t.Fatalf("根节点数量 = %d, want 1", len(tree))
	}
	if tree[0].Name != "系统管理" {
		t.Errorf("根节点名称: %s", tree[0].Name)
	}
	// Urls 应被清空
	if len(tree[0].Urls) != 0 {
		t.Errorf("规范化后 Urls 应被清空")
	}
	// 有 Urls 的节点 Auth 应设为 2
	if tree[0].Children[0].Auth != 2 {
		t.Errorf("用户列表 Auth = %d, want 2", tree[0].Children[0].Auth)
	}
	// 原树不应被修改
	if fullTree[0].Urls != nil {
		t.Error("原树不应被修改")
	}

	// 创建默认有权限模板
	adminTree := sharkauth.NormalizeAuthTree(fullTree, 1)
	if adminTree[0].Children[1].Auth != 1 {
		t.Errorf("管理员模板用户详情 Auth = %d, want 1", adminTree[0].Children[1].Auth)
	}
}

func TestPruneAuth(t *testing.T) {
	tree := []*sharkauth.AuthNode{
		{
			Name: "系统管理",
			Children: []*sharkauth.AuthNode{
				{
					Name: "用户管理",
					Children: []*sharkauth.AuthNode{
						{Name: "用户列表", Urls: []string{"/api/user/list"}, Auth: 1},
						{Name: "用户详情", Urls: []string{"/api/user/detail"}, Auth: 0},
					},
				},
				{
					Name: "角色管理",
					Children: []*sharkauth.AuthNode{
						{Name: "角色列表", Urls: []string{"/api/role/list"}, Auth: 0},
					},
				},
			},
		},
	}

	pruned := sharkauth.PruneAuth(tree)
	if len(pruned) != 1 {
		t.Fatalf("裁剪后根节点 = %d, want 1", len(pruned))
	}
	root := pruned[0]
	if len(root.Children) != 1 {
		t.Errorf("系统管理子节点数 = %d, want 1 (角色管理应被裁剪)", len(root.Children))
	}
	userMgmt := root.Children[0]
	if len(userMgmt.Children) != 1 {
		t.Errorf("用户管理子节点数 = %d, want 1 (用户详情 Auth=0 应被裁剪)", len(userMgmt.Children))
	}
	if userMgmt.Children[0].Name != "用户列表" {
		t.Errorf("保留节点应为用户列表, got %s", userMgmt.Children[0].Name)
	}
}

func TestPruneUnauthorizedAuthTree(t *testing.T) {
	parent := []*sharkauth.AuthNode{
		{
			Name: "系统管理",
			Children: []*sharkauth.AuthNode{
				{
					Name: "用户管理",
					Children: []*sharkauth.AuthNode{
						{Name: "用户列表", Auth: 1},
						{Name: "用户详情", Auth: 1},
					},
				},
			},
		},
	}

	child := []*sharkauth.AuthNode{
		{
			Name: "系统管理",
			Children: []*sharkauth.AuthNode{
				{
					Name: "用户管理",
					Children: []*sharkauth.AuthNode{
						{Name: "用户列表", Auth: 1},
					},
				},
				{
					Name: "系统配置",
					Children: []*sharkauth.AuthNode{
						{Name: "参数设置", Auth: 1}, // 越权! 父角色无此权限
					},
				},
			},
		},
	}

	validChild := sharkauth.PruneUnauthorizedAuthTree(parent, child)
	if len(validChild) != 1 {
		t.Fatalf("裁剪后根节点 = %d", len(validChild))
	}
	root := validChild[0]
	if len(root.Children) != 1 {
		t.Errorf("系统配置应被移除, children = %d", len(root.Children))
	}
	// 用户管理下的用户详情（父有权限但子未配置）应保留（Auth=0 叶子被保留因为父 Auth=1）
	userMgmt := root.Children[0]
	t.Logf("用户管理子节点数: %d", len(userMgmt.Children))
}

func TestSyncAuthTree(t *testing.T) {
	parent := []*sharkauth.AuthNode{
		{
			Name: "系统管理",
			Children: []*sharkauth.AuthNode{
				{Name: "用户列表", Urls: []string{"/api/user/list"}, Auth: 0},
				{Name: "用户详情", Urls: []string{"/api/user/detail"}, Auth: 0},
				{Name: "角色列表", Urls: []string{"/api/role/list"}, Auth: 0},
			},
		},
	}

	child := []*sharkauth.AuthNode{
		{
			Name: "系统管理",
			Children: []*sharkauth.AuthNode{
				{Name: "用户列表", Auth: 1},
			},
		},
	}

	sharkauth.SyncAuthTree(parent, child)
	if parent[0].Children[0].Auth != 1 {
		t.Errorf("用户列表 Auth = %d, want 1 (已有权限)", parent[0].Children[0].Auth)
	}
	if parent[0].Children[1].Auth != 2 {
		t.Errorf("用户详情 Auth = %d, want 2 (无权限)", parent[0].Children[1].Auth)
	}
	if parent[0].Children[2].Auth != 2 {
		t.Errorf("角色列表 Auth = %d, want 2 (无权限)", parent[0].Children[2].Auth)
	}
}

func TestPermissions(t *testing.T) {
	tree := []*sharkauth.AuthNode{
		{
			Name: "系统管理",
			Children: []*sharkauth.AuthNode{
				{
					Name: "用户管理",
					Children: []*sharkauth.AuthNode{
						{Name: "用户列表", Urls: []string{"/api/user/list", "/api/user/search"}},
						{Name: "用户详情", Urls: []string{"/api/user/detail"}},
					},
				},
				{Name: "数据导出", Urls: []string{"/api/export/data"}},
			},
		},
	}

	urlPerms := sharkauth.Permissions(tree)
	if len(urlPerms) != 4 {
		t.Errorf("映射条目数 = %d, want 4", len(urlPerms))
	}
	if perms, ok := urlPerms["/api/user/list"]; !ok || perms[0] != "系统管理.用户管理.用户列表" {
		t.Errorf("/api/user/list 映射: %v", perms)
	}
	if perms, ok := urlPerms["/api/export/data"]; !ok || perms[0] != "系统管理.数据导出" {
		t.Errorf("/api/export/data 映射: %v", perms)
	}
}

func TestFlatten(t *testing.T) {
	tree := []*sharkauth.AuthNode{
		{
			Name: "系统管理",
			Children: []*sharkauth.AuthNode{
				{
					Name: "用户管理",
					Children: []*sharkauth.AuthNode{
						{Name: "用户列表", Auth: 1},
						{Name: "用户详情", Auth: 1},
						{Name: "用户删除", Auth: 2},
					},
				},
				{Name: "系统日志", Auth: 1},
				{Name: "系统配置", Auth: 0},
			},
		},
	}

	flat := sharkauth.Flatten(tree)
	if len(flat) != 3 {
		t.Errorf("扁平化条目数 = %d, want 3 (仅 Auth=1)", len(flat))
		for k := range flat {
			t.Logf("  %s", k)
		}
	}
	if _, ok := flat["系统管理.用户管理.用户列表"]; !ok {
		t.Error("用户列表应该存在")
	}
	if _, ok := flat["系统管理.用户管理.用户删除"]; ok {
		t.Error("用户删除 Auth=2 不应存在")
	}
	if _, ok := flat["系统管理.系统配置"]; ok {
		t.Error("系统配置 Auth=0 不应存在")
	}
}

func TestFlattenEmpty(t *testing.T) {
	flat := sharkauth.Flatten(nil)
	if len(flat) != 0 {
		t.Errorf("空树扁平化 = %d", len(flat))
	}
}
