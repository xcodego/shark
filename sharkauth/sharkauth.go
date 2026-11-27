// Package sharkauth 提供基于树形结构的权限管理模型。
//
// 权限树由 AuthNode 组成，支持归一化、裁剪、父子角色同步，以及 URL 映射和扁平化。
package sharkauth

import (
	"strings"
)

// AuthNode 权限树节点，表示一个功能模块或 API 资源。
//
// 通过 Children 构成父子层级，通过 Urls 关联 API 路径，通过 Auth 标记权限状态。
//
// 示例：
//
//	tree := []*sharkauth.AuthNode{
//	    {
//	        Name: "系统管理",
//	        Children: []*sharkauth.AuthNode{
//	            {
//	                Name: "用户管理",
//	                Children: []*sharkauth.AuthNode{
//	                    {Name: "用户列表", Urls: []string{"/api/user/list"}, Auth: 1},
//	                    {Name: "用户详情", Urls: []string{"/api/user/detail"}, Auth: 2},
//	                },
//	            },
//	        },
//	    },
//	}
//
//	data, _ := json.Marshal(tree)   // 存库
//	json.Unmarshal(data, &loaded)   // 读取
type AuthNode struct {
	// Name 节点名称，如 "用户管理"、"用户列表"。与父节点名称组合构成完整路径，用于 Permissions、Flatten 等函数。
	Name string `json:"name,omitempty"`

	// Children 子节点列表，父节点的权限范围包含所有子节点，支持任意层级嵌套。
	Children []*AuthNode `json:"children,omitempty"`

	// Urls 关联的 API 路径列表，由 Permissions 转换为 URL → 权限路径映射。
	Urls []string `json:"urls,omitempty"`

	// Auth 权限状态：
	//   0: 未设置，编辑态初始值；叶子节点为 0 时会被 PruneAuth 删除
	//   1: 有权限
	//   2: 无权限，节点可见但不可访问（前端可置灰）
	Auth int `json:"auth,omitempty"`
}

// NormalizeAuthTree 深拷贝并规范化权限树，返回与原树无共享引用的新树。
//
// 规则：
//  1. DFS 深拷贝所有节点，原树不变；
//  2. 清空所有节点的 Urls，结果只保留结构和 Auth；
//  3. 原节点 Urls 非空的，Auth 统一设为 auth；其余节点 Auth 为 0。
//
// 示例：
//
//	auditorTree := sharkauth.NormalizeAuthTree(fullTree, 2) // 审核员模板：默认全部无权限
//	adminTree := sharkauth.NormalizeAuthTree(fullTree, 1)   // 管理员模板：默认全部有权限
func NormalizeAuthTree(nodes []*AuthNode, auth int) []*AuthNode {
	// dfs 深度优先克隆每个节点
	var dfs func(n *AuthNode) *AuthNode
	dfs = func(n *AuthNode) *AuthNode {
		if n == nil {
			return nil
		}
		newNode := &AuthNode{
			Name: n.Name,
		}
		// 原节点关联了 URL，说明是实际功能节点，设置 Auth
		if len(n.Urls) > 0 {
			newNode.Auth = auth
		}
		// 规范化后的树不保留 URL
		newNode.Urls = nil
		if len(n.Children) > 0 {
			newNode.Children = make([]*AuthNode, 0, len(n.Children))
			for _, child := range n.Children {
				newNode.Children = append(newNode.Children, dfs(child))
			}
		}
		return newNode
	}
	res := make([]*AuthNode, 0, len(nodes))
	for _, n := range nodes {
		res = append(res, dfs(n))
	}
	return res
}

// PruneAuth 拷贝并裁剪权限树，自底向上删除未编辑的无效节点（节点为新建，Urls 切片与原树共享）。
//
// 删除条件（同时满足）：Auth 为 0，且裁剪后没有剩余子节点。
// 因此未编辑的叶子会被删除，父节点若因此变成空叶子且 Auth 为 0 也会被删除。
// 原树不被修改。
//
// 用途：提交前清理、减小存储体积、展示前隐藏空的中间节点。
//
// 示例：
//
//	// "用户列表" Auth=1，"用户详情" Auth=0，"角色管理" 下的 "角色列表" Auth=0
//	pruned := sharkauth.PruneAuth(tree)
//	// 结果：系统管理 → 用户管理 → 用户列表(1)，"角色管理" 整棵子树被删除
func PruneAuth(nodes []*AuthNode) []*AuthNode {
	// dfs 后序遍历，先处理子节点再判断当前节点
	var dfs func(n *AuthNode) *AuthNode
	dfs = func(n *AuthNode) *AuthNode {
		if n == nil {
			return nil
		}
		newChildren := make([]*AuthNode, 0)
		for _, c := range n.Children {
			if nc := dfs(c); nc != nil {
				newChildren = append(newChildren, nc)
			}
		}
		// 子节点全部被裁剪后视为叶子；叶子且 Auth 为 0 则删除
		isLeaf := len(newChildren) == 0
		if isLeaf && n.Auth == 0 {
			return nil
		}
		newNode := &AuthNode{
			Name:     n.Name,
			Urls:     n.Urls,
			Auth:     n.Auth,
			Children: newChildren,
		}

		return newNode
	}
	res := make([]*AuthNode, 0)
	for _, n := range nodes {
		if nn := dfs(n); nn != nil {
			res = append(res, nn)
		}
	}
	return res
}

// PruneUnauthorizedAuthTree 以父权限树为上限裁剪子权限树，使子树不越权。
//
// 按节点的完整名称路径在 parent 中查找对应节点，按以下规则删除，返回新树（不修改 child）：
//   - parent 中不存在该路径：删除（父角色无此节点）；
//   - child 中的叶子节点，且 parent 对应节点 Auth 不为 1：删除；
//   - 其余节点保留，并递归处理子节点。
//
// 效果：父角色回收某权限后，子角色的该权限自动失效。
//
// 示例：
//
//	// parent：系统管理 → 用户管理 → 用户列表(1)、用户详情(1)
//	// child： 系统管理 → 用户管理 → 用户列表(1)；系统管理 → 系统配置 → 参数设置(1)
//	valid := sharkauth.PruneUnauthorizedAuthTree(parentTree, childTree)
//	// 结果：系统管理 → 用户管理 → 用户列表(1)；"系统配置" 因 parent 中不存在被删除
func PruneUnauthorizedAuthTree(parent, child []*AuthNode) []*AuthNode {
	// find 按名称路径在权限树中查找节点，names 形如 ["系统管理", "用户管理", "用户列表"]
	var find func(nodes []*AuthNode, names []string) *AuthNode
	find = func(nodes []*AuthNode, names []string) *AuthNode {
		if len(names) == 0 {
			return nil
		}
		for _, n := range nodes {
			if n.Name != names[0] {
				continue
			}
			if len(names) == 1 {
				return n
			}
			return find(n.Children, names[1:])
		}
		return nil
	}

	// dfs 遍历子权限树，path 为当前节点的父路径，用于在 parent 中定位
	var dfs func(nodes []*AuthNode, path []string) []*AuthNode
	dfs = func(nodes []*AuthNode, path []string) []*AuthNode {
		res := make([]*AuthNode, 0)
		for _, n := range nodes {
			cur := append(append([]string{}, path...), n.Name)
			p := find(parent, cur)
			// parent 中不存在该节点，越权，删除
			if p == nil {
				continue
			}
			// 叶子节点且 parent 对应节点无权限，删除
			isLeaf := len(n.Children) == 0
			if isLeaf && p.Auth != 1 {
				continue
			}
			newNode := &AuthNode{
				Name: n.Name,
				Urls: n.Urls,
				Auth: n.Auth,
			}
			newNode.Children = dfs(n.Children, cur)
			res = append(res, newNode)
		}
		return res
	}
	return dfs(child, []string{})
}

// SyncAuthTree 以 parent 为权限上限，原地标记 parent 中终端节点在 child 中的拥有状态。
//
// 终端节点指无子节点，或 Auth 已非 0 的节点。对每个终端节点：
//   - child 中存在对应路径：Auth=1（已拥有，前端勾选）；
//   - 否则：Auth=2（未拥有，前端可见但不勾选）。
//
// 中间节点（有子节点且 Auth=0）保持不变，用于展开。
// child 为空时，所有终端节点 Auth 均为 2。
//
// 注意：直接修改 parent，无返回值。如需保留原 parent，请先深拷贝（如 NormalizeAuthTree）。
//
// 示例：
//
//	// parent：系统管理 → {用户列表, 用户详情, 角色列表}（Auth 均为 0）
//	// child： 系统管理 → {用户列表(1)}
//	sharkauth.SyncAuthTree(parentTree, childTree)
//	// 用户列表 Auth=1；用户详情、角色列表 Auth=2
//	// 前端渲染：1 勾选，2 未勾选，0 不显示 checkbox（中间层）
func SyncAuthTree(parent, child []*AuthNode) {
	// find 按名称路径在权限树中查找节点（与 PruneUnauthorizedAuthTree 中的 find 一致）
	var find func(nodes []*AuthNode, names []string) *AuthNode
	find = func(nodes []*AuthNode, names []string) *AuthNode {
		if len(names) == 0 {
			return nil
		}
		for _, n := range nodes {
			if n.Name != names[0] {
				continue
			}
			if len(names) == 1 {
				return n
			}
			return find(n.Children, names[1:])
		}
		return nil
	}

	// dfs 遍历 parent，为终端节点设置 Auth
	var dfs func(nodes []*AuthNode, path []string)
	dfs = func(nodes []*AuthNode, path []string) {
		for _, n := range nodes {
			cur := append(path, n.Name)
			if len(n.Children) == 0 || n.Auth != 0 {
				if find(child, cur) != nil {
					n.Auth = 1
				} else {
					n.Auth = 2
				}
			}
			dfs(n.Children, cur)
		}
	}
	dfs(parent, []string{})
}

// Permissions 将权限树转换为 URL → 权限路径列表的映射。
//
// 遍历所有节点，将每个节点的 Urls 映射到其从根到该节点的点分隔路径（如 "系统管理.用户管理.用户列表"）。
// 同一 URL 可出现在多个节点下，因此值为切片。该函数不考虑 Auth。
//
// 用途：API 鉴权中间件根据请求 URL 快速查找所需权限。
//
// 示例：
//
//	urlPermMap := sharkauth.Permissions(tree)
//	// {"/api/user/list": ["系统管理.用户管理.用户列表"], "/api/export/data": ["系统管理.数据导出"], ...}
func Permissions(nodes []*AuthNode) map[string][]string {
	res := map[string][]string{}
	// dfs 遍历权限树，收集 URL 到路径的映射
	var dfs func(nodes []*AuthNode, path []string)
	dfs = func(nodes []*AuthNode, path []string) {
		for _, n := range nodes {
			cur := append(path, n.Name)
			key := strings.Join(cur, ".")
			for _, url := range n.Urls {
				res[url] = append(res[url], key)
			}
			dfs(n.Children, cur)
		}
	}
	dfs(nodes, []string{})
	return res
}

// Flatten 将权限树扁平化为 map，仅收录 Auth=1 的节点。
//
// key 为节点从根到自身的点分隔完整路径，value 固定为 1。Auth 为 0 或 2 的节点不收录；
// 中间节点若 Auth=1 也会被收录。
//
// 用途：序列化后存入 Redis 或数据库，实现 O(1) 权限判断；前端也可直接查表。
//
// 示例：
//
//	flat := sharkauth.Flatten(tree)
//	// {"系统管理.用户管理.用户列表": 1, "系统管理.系统日志": 1}
//	if _, ok := flat["系统管理.用户管理.用户列表"]; ok {
//	    // 有权限
//	}
func Flatten(nodes []*AuthNode) map[string]int {
	res := make(map[string]int)
	// dfs 遍历权限树，path 为父节点完整路径
	var dfs func(node *AuthNode, path string)
	dfs = func(node *AuthNode, path string) {
		cur := node.Name
		if path != "" {
			cur = path + "." + node.Name
		}
		if node.Auth == 1 {
			res[cur] = 1
		}
		for _, child := range node.Children {
			dfs(child, cur)
		}
	}
	for _, n := range nodes {
		dfs(n, "")
	}
	return res
}
