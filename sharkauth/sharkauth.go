// Package sharkauth 提供基于树形结构的权限管理模型。
//
// 核心概念：
//
//	权限体系采用多叉树结构建模，每个节点代表一个功能模块或 API 资源。
//	通过 Name 标识节点名称，通过 Children 构成父子层级关系，通过 Urls 关联 API 路径，
//	通过 Auth（三位状态码：0=未设置, 1=有权限, 2=无权限）标记权限状态。
//
// 核心功能：
//  1. AuthNode 结构体：权限树节点定义（支持 JSON 序列化）
//  2. NormalizeAuthTree：深拷贝并统一设置权限状态（初始化权限模板）
//  3. PruneAuth：递归裁剪无意义的中间节点（精简权限树）
//  4. PruneUnauthorizedAuthTree：父子权限继承裁剪（父角色回收后子角色自动缩小）
//  5. SyncAuthTree：对比父/子权限树生成带状态标记的编辑树（前端权限界面渲染）
//  6. Permissions：将树形权限转换为 URL→权限路径 的 O(1) 查表结构（API 鉴权中间件）
//  7. Flatten：将权限树扁平化为路径→1 的 map（Redis 缓存/前端判断）
//
// 使用场景：
//   - RBAC 角色权限管理
//   - 父子角色权限继承
//   - 前端菜单树渲染（含选中/禁用状态）
//   - API 鉴权中间件
//
// 树形结构示例：
//
//	系统管理                        ← 根节点
//	├── 用户管理                    ← 中间节点（折叠层）
//	│   ├── 用户列表  Urls: ["/api/user/list"]    Auth: 1（有权限）
//	│   └── 用户详情  Urls: ["/api/user/detail"]  Auth: 2（无权限但可见）
//	└── 角色管理
//	    └── 角色列表  Urls: ["/api/role/list"]     Auth: 1
//
// 使用示例：
//
//	// 构建完整功能树（定义系统所有功能）
//	fullTree := []*sharkauth.AuthNode{
//	    {
//	        Name: "系统管理",
//	        Children: []*sharkauth.AuthNode{
//	            {
//	                Name: "用户管理",
//	                Children: []*sharkauth.AuthNode{
//	                    {Name: "用户列表", Urls: []string{"/api/user/list"}},
//	                    {Name: "用户详情", Urls: []string{"/api/user/detail"}},
//	                },
//	            },
//	        },
//	    },
//	}
//
//	// 初始化子角色权限模板（所有功能默认 Auth=2 无权限）
//	childTree := sharkauth.NormalizeAuthTree(fullTree, 2)
//
//	// 设置子角色部分权限
//	childTree[0].Children[0].Children[0].Auth = 1 // 用户列表 → 有权限
//
//	// 裁剪无意义节点
//	pruned := sharkauth.PruneAuth(childTree)
//
//	// 转换为 URL→权限映射（用于鉴权中间件）
//	urlPermMap := sharkauth.Permissions(fullTree)
//	// urlPermMap["/api/user/list"] → ["系统管理.用户管理.用户列表"]
package sharkauth

import (
	"strings"
)

// AuthNode 权限树节点，表示权限体系中的一个功能模块或 API 资源。
//
// 权限树是一个多叉树结构，通过 Name 标识节点名称，通过 Children 构成层级父子关系，
// 通过 Urls 关联 API 接口路径，通过 Auth 标记权限状态。
//
// Auth 字段含义（三位状态码）：
//   - 0: 未设置/默认值，表示该节点的权限状态未确定（用于编辑态初始值）
//   - 1: 拥有权限，表示用户/角色对该节点下的资源具有访问权限
//   - 2: 无权限，表示显示该节点但明确不具有访问权限（前端可渲染但置灰）
//
// 使用场景示例：
//   - 角色权限管理：为不同角色配置可访问的 URL 资源
//   - 父子角色继承：子角色权限需在父角色允许范围内裁剪
//   - 前端菜单渲染：根据 Auth 值决定菜单项的选中/禁用状态
//
// 使用示例：
//
//	// 构建一个简单的权限树
//	tree := []*sharkauth.AuthNode{
//	    {
//	        Name: "系统管理",
//	        Children: []*sharkauth.AuthNode{
//	            {
//	                Name: "用户管理",
//	                Children: []*sharkauth.AuthNode{
//	                    {
//	                        Name: "用户列表",
//	                        Urls: []string{"/api/user/list", "/api/user/search"},
//	                        Auth: 1, // 有权限
//	                    },
//	                    {
//	                        Name: "用户详情",
//	                        Urls: []string{"/api/user/detail"},
//	                        Auth: 2, // 无权限但显示
//	                    },
//	                },
//	            },
//	        },
//	    },
//	}
//
//	// JSON 序列化（存入数据库）
//	data, _ := json.Marshal(tree)
//	// 反序列化
//	var loaded []*sharkauth.AuthNode
//	json.Unmarshal(data, &loaded)
type AuthNode struct {
	// Name 节点名称，通常是功能模块名称（如 "用户管理"、"订单管理"）。
	// 在整个权限树中，Name 与其父节点路径组合构成唯一标识。
	// 示例值："用户列表"、"数据导出"、"系统管理"
	Name string `json:"name,omitempty"`

	// Children 子节点列表，构成权限树的层级结构。
	// 父节点的权限范围包含所有子节点。最多支持无限层级嵌套。
	Children []*AuthNode `json:"children,omitempty"`

	// Urls 该节点关联的 API 接口路径列表。
	// 一个权限节点可以对应多个 URL，URL 到权限路径的映射由 Permissions() 函数生成。
	// 示例值：["/api/user/list", "/api/user/detail", "/api/order/create"]
	Urls []string `json:"urls,omitempty"`

	// Auth 权限状态标志。
	//   0: 未设置/默认值 — 权限编辑态的初始值，表示用户尚未操作此节点的权限
	//   1: 有权限 — 用户/角色可以访问该节点下的 URL 资源
	//   2: 无权限 — 节点可见但不可访问（前端渲染时置灰）
	Auth int `json:"auth,omitempty"`
}

// NormalizeAuthTree 深拷贝并规范化权限树。
//
// 执行以下操作：
//  1. 对传入的权限树进行完全深拷贝（DFS 递归克隆），确保不修改原始数据
//  2. 清空所有节点的 Urls 字段（规范化后的树仅保留结构和 Auth 状态）
//  3. 对于原始树中 Urls 不为空的节点（即有实际 URL 关联的功能节点），
//     将其 Auth 统一设置为参数 auth 指定的值
//
// 参数：
//   - nodes: 原始权限树根节点列表（允许多棵树并列，如多个一级菜单）
//   - auth:  对所有有 URL 的节点统一设置的 Auth 值（如 2 表示全部无权限）
//
// 返回值：
//   - 深拷贝并规范化后的全新权限树（与原树无共享引用，修改返回值不会影响原数据）
//
// 使用场景：
//   - 创建角色的初始权限模板：将完整功能树拷贝一份，所有功能默认 Auth=2（无权限）
//   - 重置角色权限：基于完整功能树重新生成权限编辑界面
//
// 使用示例：
//
//	// 完整功能树（定义系统所有功能模块）
//	fullTree := []*sharkauth.AuthNode{
//	    {
//	        Name: "系统管理",
//	        Children: []*sharkauth.AuthNode{
//	            {Name: "用户列表", Urls: []string{"/api/user/list"}},
//	            {Name: "用户详情", Urls: []string{"/api/user/detail"}},
//	            {Name: "角色管理",
//	                Children: []*sharkauth.AuthNode{
//	                    {Name: "角色列表", Urls: []string{"/api/role/list"}},
//	                },
//	            },
//	        },
//	    },
//	}
//
//	// 创建"审核员"角色的初始权限模板（所有功能默认无权限）
//	auditorTree := sharkauth.NormalizeAuthTree(fullTree, 2)
//
//	// 为审核员手动配置部分权限
//	// auditorTree[0].Children[0].Auth = 1  // 用户列表 → 有权限
//	// auditorTree[0].Children[2].Children[0].Auth = 1  // 角色列表 → 有权限
//
//	// 另一场景：创建"管理员"角色的初始模板（所有功能默认有权限）
//	adminTree := sharkauth.NormalizeAuthTree(fullTree, 1)
func NormalizeAuthTree(nodes []*AuthNode, auth int) []*AuthNode {
	// dfs 深度优先遍历克隆每个节点
	var dfs func(n *AuthNode) *AuthNode
	dfs = func(n *AuthNode) *AuthNode {
		if n == nil {
			return nil
		}
		// 创建新节点，仅拷贝 Name
		newNode := &AuthNode{
			Name: n.Name,
		}
		// 如果原节点关联了 URL，则设置 Auth 为传入值（表示该节点是"有实际功能"的节点）
		if len(n.Urls) > 0 {
			newNode.Auth = auth
		}
		// 清空 Urls：规范化后的树不需要 URL 信息
		newNode.Urls = nil
		// 递归克隆子节点
		if len(n.Children) > 0 {
			newNode.Children = make([]*AuthNode, 0, len(n.Children))
			for _, child := range n.Children {
				newNode.Children = append(newNode.Children, dfs(child))
			}
		}
		return newNode
	}
	// 遍历根节点数组并分别克隆
	res := make([]*AuthNode, 0, len(nodes))
	for _, n := range nodes {
		res = append(res, dfs(n))
	}
	return res
}

// PruneAuth 递归裁剪权限树，移除无意义的中间节点和未配置的叶子节点。
//
// 执行以下操作：
//  1. 对权限树进行完全深拷贝，不修改原数据
//  2. 从叶子节点开始自底向上递归裁剪（后序遍历）
//  3. 删除同时满足以下两个条件的节点：
//     - Auth 值为 0（未设置权限状态，即用户未编辑过的节点）
//     - 没有任何保留的有效子节点（即裁剪后成为空叶子节点）
//  4. 保留 Auth 不为 0 或存在有效子节点的节点
//
// 参数：
//   - nodes: 待裁剪的权限树根节点列表
//
// 返回值：
//   - 裁剪后的权限树（去除了所有未编辑过且无有效子节点的中间层节点）
//
// 使用场景：
//   - 提交权限配置前清理：去除用户未操作过的空节点，减小存储体积
//   - 权限树展示前精简：隐藏未配置权限的中间层折叠节点
//   - 数据库存储前压缩：减少 JSON 数据量
//
// 使用示例：
//
//	// 假设用户只编辑了"用户列表"权限
//	tree := []*sharkauth.AuthNode{
//	    {
//	        Name: "系统管理",
//	        Children: []*sharkauth.AuthNode{
//	            {
//	                Name: "用户管理",
//	                Children: []*sharkauth.AuthNode{
//	                    {Name: "用户列表", Urls: []string{"/api/user/list"}, Auth: 1},
//	                    // 用户详情 Auth=0（未编辑）
//	                    {Name: "用户详情", Urls: []string{"/api/user/detail"}, Auth: 0},
//	                },
//	            },
//	            {
//	                Name: "角色管理", // Auth=0 且子节点全被裁剪 → 删除
//	                Children: []*sharkauth.AuthNode{
//	                    {Name: "角色列表", Urls: []string{"/api/role/list"}, Auth: 0},
//	                },
//	            },
//	        },
//	    },
//	}
//
//	// 裁剪后仅保留有 Auth=1 的节点及其有效路径
//	pruned := sharkauth.PruneAuth(tree)
//	// pruned → [系统管理 → 用户管理 → 用户列表(Auth=1)]
//	// 角色管理及其子节点因 Auth 全为 0 被彻底删除
func PruneAuth(nodes []*AuthNode) []*AuthNode {
	// dfs 后序遍历，从叶子向上裁剪
	var dfs func(n *AuthNode) *AuthNode
	dfs = func(n *AuthNode) *AuthNode {
		if n == nil {
			return nil
		}
		// 第一步：递归处理所有子节点，收集有效的子节点
		newChildren := make([]*AuthNode, 0)
		for _, c := range n.Children {
			if nc := dfs(c); nc != nil {
				newChildren = append(newChildren, nc)
			}
		}
		// 第二步：判断是否为叶子节点（所有子节点都被裁剪掉了）
		isLeaf := len(newChildren) == 0
		// 叶子节点且 Auth 为 0 → 无意义节点，返回 nil 表示删除
		if isLeaf && n.Auth == 0 {
			return nil
		}
		// 第三步：保留当前节点，并携带裁剪后的子节点列表
		newNode := &AuthNode{
			Name:     n.Name,
			Urls:     n.Urls,
			Auth:     n.Auth,
			Children: newChildren,
		}

		return newNode
	}
	// 处理根节点数组
	res := make([]*AuthNode, 0)
	for _, n := range nodes {
		if nn := dfs(n); nn != nil {
			res = append(res, nn)
		}
	}
	return res
}

// PruneUnauthorizedAuthTree 根据父权限树裁剪子权限树，实现"父回收→子自动缩小"。
//
// 核心逻辑：
//
//	以父角色的权限树（parent）为基准，遍历子角色的权限树（child），
//	删除所有不在父角色权限范围内的节点。即"父角色被回收了某权限后，
//	子角色的该权限也自动失效"。
//
// 处理规则：
//  1. 递归遍历子权限树的每个节点
//  2. 在父权限树中按完整路径查找对应节点
//  3. 如果当前节点在父权限树中不存在 → 删除该节点（父角色没有此权限）
//  4. 如果是叶子节点且父节点 Auth 不为 1 → 删除该节点（父角色无此权限）
//  5. 否则保留该节点，并递归裁剪其子节点
//
// 参数：
//   - parent: 父角色的权限树（作为裁剪基准，定义了权限的最大边界）
//   - child:  子角色的权限树（待裁剪的权限树，裁剪后自动缩小到父角色允许范围内）
//
// 返回值：
//   - 裁剪后的子角色有效权限树，确保不超出父角色权限范围
//
// 使用场景：
//   - 父角色权限被回收后，同步裁剪所有子角色权限
//   - 防止子角色保留越权权限（权限继承时的安全兜底）
//   - 生成子角色最终有效权限树（用于权限比对和存储）
//
// 使用示例：
//
//	// 父角色权限树（管理员只有"用户管理"权限，没有"系统配置"权限）
//	parentTree := []*sharkauth.AuthNode{
//	    {
//	        Name: "系统管理",
//	        Children: []*sharkauth.AuthNode{
//	            {
//	                Name: "用户管理",
//	                Children: []*sharkauth.AuthNode{
//	                    {Name: "用户列表", Auth: 1},
//	                    {Name: "用户详情", Auth: 1},
//	                },
//	            },
//	            // 注意：父角色没有"系统配置"节点
//	        },
//	    },
//	}
//
//	// 子角色权限树（之前可能配置了"系统配置"的权限）
//	childTree := []*sharkauth.AuthNode{
//	    {
//	        Name: "系统管理",
//	        Children: []*sharkauth.AuthNode{
//	            {
//	                Name: "用户管理",
//	                Children: []*sharkauth.AuthNode{
//	                    {Name: "用户列表", Auth: 1},
//	                },
//	            },
//	            {
//	                Name: "系统配置",
//	                Children: []*sharkauth.AuthNode{
//	                    {Name: "参数设置", Auth: 1}, // 越权！
//	                },
//	            },
//	        },
//	    },
//	}
//
//	// 裁剪后，"系统配置"节点将被删除（父角色没有该权限）
//	validChild := sharkauth.PruneUnauthorizedAuthTree(parentTree, childTree)
//	// validChild → [系统管理 → 用户管理 → 用户列表(Auth=1)]
func PruneUnauthorizedAuthTree(parent, child []*AuthNode) []*AuthNode {
	// find 在权限树中按路径查找节点
	// names 是一个从根到目标节点的名称路径数组
	// 例如：["系统管理", "用户管理", "用户列表"]
	var find func(nodes []*AuthNode, names []string) *AuthNode

	find = func(nodes []*AuthNode, names []string) *AuthNode {
		if len(names) == 0 {
			return nil
		}
		// 在节点列表中查找名称匹配的第一个节点
		for _, n := range nodes {
			if n.Name != names[0] {
				continue
			}
			// 路径已经匹配到最后一层，返回该节点
			if len(names) == 1 {
				return n
			}
			// 继续向子节点查找剩余路径
			return find(n.Children, names[1:])
		}
		return nil
	}

	// dfs 深度优先遍历子权限树进行裁剪
	// path 记录从根到当前节点的名称路径，用于在父树中定位对应节点
	var dfs func(nodes []*AuthNode, path []string) []*AuthNode
	dfs = func(nodes []*AuthNode, path []string) []*AuthNode {
		res := make([]*AuthNode, 0)
		for _, n := range nodes {
			// 构建当前节点的完整路径
			cur := append(append([]string{}, path...), n.Name)
			// 在父权限树中查找对应节点
			p := find(parent, cur)
			// 父权限树中不存在该路径 → 当前节点越权，跳过（删除）
			if p == nil {
				continue
			}
			// 判断是否为叶子节点
			isLeaf := len(n.Children) == 0
			// 叶子节点且父节点 Auth 不为 1（父角色无此权限）→ 跳过（删除）
			if isLeaf && p.Auth != 1 {
				continue
			}
			// 保留当前节点
			newNode := &AuthNode{
				Name: n.Name,
				Urls: n.Urls,
				Auth: n.Auth,
			}
			// 递归裁剪子节点
			newNode.Children = dfs(n.Children, cur)
			res = append(res, newNode)
		}
		return res
	}
	return dfs(child, []string{})
}

// SyncAuthTree 根据父权限树和子权限树，生成带状态标记的子角色权限编辑树。
//
// 核心逻辑：
//
//	遍历父权限树（定义最大权限范围），对每个终端节点（叶子节点或 Auth≠0 的节点）
//	检查该节点在子角色当前权限树中是否存在：
//	  - 存在 → 设置 Auth=1（子角色已有该权限，前端渲染为选中态）
//	  - 不存在 → 设置 Auth=2（子角色没有该权限，前端渲染为未选态但可见）
//
// 参数：
//   - parent: 父角色的完整权限树（定义了子角色可以拥有的最大权限范围）
//   - child:  子角色当前的权限树（如为空数组，则所有节点 Auth=2）
//
// 返回值：
//   - 直接修改 parent 树并返回（原地修改！），每个终端节点的 Auth 标记了子角色的权限状态
//
// 重要提示：
//
//	该函数会直接修改传入的 parent 参数（原地修改）！
//	如果需要在后续流程中保留原始 parent 树，调用前请先深拷贝。
//
// 使用场景：
//   - 权限编辑页面渲染：显示完整权限列表并用勾选框标记子角色已有权限
//   - 权限差异对比：快速识别子角色有哪些额外的或缺失的权限
//   - 角色权限配置界面初始化：生成前端权限树组件的数据源
//
// 使用示例：
//
//	// 父角色完整权限树（定义了子角色的权限上限）
//	parentTree := []*sharkauth.AuthNode{
//	    {
//	        Name: "系统管理",
//	        Children: []*sharkauth.AuthNode{
//	            {Name: "用户列表", Urls: []string{"/api/user/list"}, Auth: 0},
//	            {Name: "用户详情", Urls: []string{"/api/user/detail"}, Auth: 0},
//	            {Name: "角色列表", Urls: []string{"/api/role/list"}, Auth: 0},
//	        },
//	    },
//	}
//
//	// 子角色当前权限（只有"用户列表"）
//	childTree := []*sharkauth.AuthNode{
//	    {
//	        Name: "系统管理",
//	        Children: []*sharkauth.AuthNode{
//	            {Name: "用户列表", Auth: 1},
//	        },
//	    },
//	}
//
//	// 生成编辑树（原地修改 parentTree）
//	editTree := sharkauth.SyncAuthTree(parentTree, childTree)
//	// editTree 结果：
//	//   - 用户列表: Auth=1（已选中）
//	//   - 用户详情: Auth=2（未选中）
//	//   - 角色列表: Auth=2（未选中）
//
//	// 前端可根据 Auth 值渲染：
//	//   Auth=1 → checkbox checked
//	//   Auth=2 → checkbox unchecked
//	//   Auth=0 → 不显示 checkbox（中间层折叠节点）
func SyncAuthTree(parent, child []*AuthNode) {
	// find 在权限树中按路径查找节点（与 PruneUnauthorizedAuthTree 中的 find 逻辑一致）
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

	// dfs 遍历父权限树，为每个需要标记的节点设置 Auth 状态
	var dfs func(nodes []*AuthNode, path []string)
	dfs = func(nodes []*AuthNode, path []string) {
		for _, n := range nodes {
			// 构建当前节点路径
			cur := append(path, n.Name)
			// 判断是否为"终端节点"：无子节点 或 Auth 已非 0
			// 只有终端节点才需要标记权限状态（中间层节点保留原状用于展开）
			if len(n.Children) == 0 || n.Auth != 0 {
				// 在子权限树中查找对应节点
				if find(child, cur) != nil {
					n.Auth = 1 // 子角色拥有该权限
				} else {
					n.Auth = 2 // 子角色未拥有该权限
				}
			}
			// 递归处理子节点
			dfs(n.Children, cur)
		}
	}
	dfs(parent, []string{})
}

// Permissions 将权限树转换为 URL → 权限路径列表 的映射表。
//
// 递归遍历权限树的所有节点，收集每个节点的 Urls 和从根到该节点的点分隔路径，
// 建立 URL → [权限路径...] 的映射关系。同一个 URL 可能属于多个权限路径。
//
// 参数：
//   - nodes: 权限树根节点列表
//
// 返回值：
//   - map[string][]string: key 为 API URL（如 "/api/user/list"），
//     value 为该 URL 所属的所有权限路径（如 ["系统管理.用户管理.用户列表"]）
//
// 使用场景：
//   - API 鉴权中间件：根据请求 URL 快速查找需要的权限路径，实现 O(1) 鉴权
//   - 权限检查缓存：将树形权限结构转换为高效查表结构
//   - URL 批量鉴权：一次性建立完整的 URL → 权限 映射关系
//
// 使用示例：
//
//	// 构建权限树
//	tree := []*sharkauth.AuthNode{
//	    {
//	        Name: "系统管理",
//	        Children: []*sharkauth.AuthNode{
//	            {
//	                Name: "用户管理",
//	                Children: []*sharkauth.AuthNode{
//	                    {Name: "用户列表", Urls: []string{"/api/user/list", "/api/user/search"}},
//	                    {Name: "用户详情", Urls: []string{"/api/user/detail"}},
//	                },
//	            },
//	            {
//	                Name: "数据导出",
//	                Urls: []string{"/api/export/data"},
//	            },
//	        },
//	    },
//	}
//
//	// 生成 URL→权限 映射
//	urlPermMap := sharkauth.Permissions(tree)
//	// urlPermMap 结果:
//	// {
//	//   "/api/user/list":   ["系统管理.用户管理.用户列表"],
//	//   "/api/user/search": ["系统管理.用户管理.用户列表"],
//	//   "/api/user/detail": ["系统管理.用户管理.用户详情"],
//	//   "/api/export/data": ["系统管理.数据导出"],
//	// }
//
//	// API 鉴权中间件使用
//	func AuthMiddleware(urlPermMap map[string][]string, userPerms map[string]any) gin.HandlerFunc {
//	    return func(c *gin.Context) {
//	        url := c.Request.URL.Path
//	        requiredPerms, ok := urlPermMap[url]
//	        if !ok {
//	            c.Next() // URL 不在权限控制范围内，放行
//	            return
//	        }
//	        for _, perm := range requiredPerms {
//	            if _, has := userPerms[perm]; has {
//	                c.Next() // 用户拥有任一所需权限，放行
//	                return
//	            }
//	        }
//	        c.JSON(403, gin.H{"error": "无权限"})
//	        c.Abort()
//	    }
//	}
func Permissions(nodes []*AuthNode) map[string][]string {
	res := map[string][]string{}
	// dfs 遍历权限树，收集 URL 到路径的映射
	var dfs func(nodes []*AuthNode, path []string)
	dfs = func(nodes []*AuthNode, path []string) {
		for _, n := range nodes {
			// 构建当前节点的完整权限路径（如 "系统管理.用户管理.用户列表"）
			cur := append(path, n.Name)
			key := strings.Join(cur, ".")
			// 将该节点的所有 URL 映射到当前权限路径
			for _, url := range n.Urls {
				res[url] = append(res[url], key)
			}
			// 递归处理子节点
			dfs(n.Children, cur)
		}
	}
	dfs(nodes, []string{})
	return res
}

// Flatten 将权限树扁平化为一层 map 结构，仅提取有权限的节点。
//
// 递归遍历权限树，仅收集 Auth 值为 1（有权限）的节点，
// 将其完整点分隔路径作为 key、固定值 1 作为 value，构建扁平化映射表。
//
// 参数：
//   - nodes: 权限树根节点列表
//
// 返回值：
//   - map[string]any: key 为具有权限的节点的完整点分隔路径，value 固定为 1
//
// 使用场景：
//   - Redis 缓存存储：序列化为 JSON 存入 Redis Hash，实现高性能权限查询
//   - 权限比对：O(1) 查表判断某个完整路径是否有权限
//   - 数据库存储用户权限：以 JSON 格式存储用户有权限的路径集合
//   - 前端权限判断：前端只需检查路径是否在扁平 map 中即可
//
// 使用示例：
//
//	// 有权限的树
//	tree := []*sharkauth.AuthNode{
//	    {
//	        Name: "系统管理",
//	        Children: []*sharkauth.AuthNode{
//	            {
//	                Name: "用户管理",
//	                Children: []*sharkauth.AuthNode{
//	                    {Name: "用户列表", Auth: 1},
//	                    {Name: "用户详情", Auth: 1},
//	                    {Name: "用户删除", Auth: 2}, // 无权限，不会被收集
//	                },
//	            },
//	            {Name: "系统日志", Auth: 1},
//	        },
//	    },
//	}
//
//	// 扁平化
//	flat := sharkauth.Flatten(tree)
//	// flat 结果:
//	// {
//	//   "系统管理.用户管理.用户列表": 1,
//	//   "系统管理.用户管理.用户详情": 1,
//	//   "系统管理.系统日志": 1,
//	// }
//	// 注意："系统管理.用户管理.用户删除" Auth=2 未被收集
//
//	// 存入 Redis
//	data, _ := json.Marshal(flat)
//	rdb.HSet(ctx, fmt.Sprintf("user:perms:%d", userId), data)
//
//	// 权限判断（O(1)）
//	requiredPerm := "系统管理.用户管理.用户列表"
//	if _, has := flat[requiredPerm]; has {
//	    fmt.Println("有权限访问")
//	} else {
//	    fmt.Println("无权限访问")
//	}
//
//	// 前端权限判断（JavaScript）
//	// const userPerms = {"系统管理.用户管理.用户列表": 1, ...};
//	// if (userPerms["系统管理.用户管理.用户列表"]) {
//	//     showUserListButton();
//	// }
func Flatten(nodes []*AuthNode) map[string]int {
	res := make(map[string]int)
	// dfs 遍历权限树，仅收集 Auth=1 的节点路径
	var dfs func(node *AuthNode, path string)
	dfs = func(node *AuthNode, path string) {
		// 构建当前节点的完整路径
		cur := node.Name
		if path != "" {
			// 非根节点：在前面拼接父路径
			cur = path + "." + node.Name
		}
		// 仅当 Auth 为 1（有权限）时记录
		if node.Auth == 1 {
			res[cur] = 1
		}
		// 递归处理子节点
		for _, child := range node.Children {
			dfs(child, cur)
		}
	}
	// 遍历所有根节点
	for _, n := range nodes {
		dfs(n, "")
	}
	return res
}
