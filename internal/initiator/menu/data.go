package menu

import (
	"github.com/limes-cloud/kratosx/pkg/value"

	managerMenu "github.com/limes-cloud/manager/api/menu"
)

// appKeyword 本服务在 manager 侧的应用标识（与 ns_manager_v2.app.keyword 一致，统一小写）
const appKeyword = "gateway"

// 菜单节点类型（与 manager entity.MenuType* 对应）
const (
	menuTypeApi   = "A"  // 受权限控制的接口
	menuTypePage  = "M"  // 菜单页面
	menuTypeGroup = "G"  // 菜单分组
	menuTypeBasic = "BA" // 基础接口：登录即可用，无需单独授权
)

// group 构造菜单分组节点（G）。
func group(title, key, icon string, hidden bool, children ...*managerMenu.RegisterMenuNode) *managerMenu.RegisterMenuNode {
	return &managerMenu.RegisterMenuNode{
		Title:    title,
		Type:     menuTypeGroup,
		Key:      value.Pointer(key),
		Icon:     value.Pointer(icon),
		Weight:   value.Pointer[int32](0),
		IsHidden: value.Pointer(hidden),
		Children: children,
	}
}

// page 构造菜单页面节点（M）；path/component 对应前端路由与组件。
func page(title, key, icon, path, component string, children ...*managerMenu.RegisterMenuNode) *managerMenu.RegisterMenuNode {
	return &managerMenu.RegisterMenuNode{
		Title:     title,
		Type:      menuTypePage,
		Key:       value.Pointer(key),
		Icon:      value.Pointer(icon),
		Path:      value.Pointer(path),
		Component: value.Pointer(component),
		Weight:    value.Pointer[int32](0),
		IsHidden:  value.Pointer(false),
		IsCache:   value.Pointer(true),
		Children:  children,
	}
}

// basic 构造基础接口节点（BA，无需授权）。
func basic(title, api, method string) *managerMenu.RegisterMenuNode {
	return apiNode(menuTypeBasic, title, api, method, "")
}

// apiPerm 构造受权限控制的接口节点（A）。
func apiPerm(title, api, method, permission string) *managerMenu.RegisterMenuNode {
	return apiNode(menuTypeApi, title, api, method, permission)
}

// apiNode 构造接口类节点（A / BA）。
func apiNode(tp, title, api, method, permission string) *managerMenu.RegisterMenuNode {
	node := &managerMenu.RegisterMenuNode{
		Title:  title,
		Type:   tp,
		Api:    value.Pointer(api),
		Method: value.Pointer(method),
		Weight: value.Pointer[int32](0),
	}
	if permission != "" {
		node.Permission = value.Pointer(permission)
	}
	return node
}

// menus 注册到 manager 的菜单/权限节点（append 模式：只新增/更新，不删旧节点）。
var menus = []*managerMenu.RegisterMenuNode{
	group("网关配置", "Gateway", "iconsys-lujing", false,
		page("路由管理", "GatewayEndpoints", "iconsys-lujing", "/gateway/endpoints", "/gateway/endpoints",
			apiPerm("查询路由", "/gateway/api/endpoints", "GET", "gateway:endpoint:list"),
			apiPerm("新增路由", "/gateway/api/endpoints", "POST", "gateway:endpoint:add"),
			apiPerm("修改路由", "/gateway/api/endpoints", "PUT", "gateway:endpoint:update"),
			apiPerm("删除路由", "/gateway/api/endpoints", "DELETE", "gateway:endpoint:delete"),
			apiPerm("发布配置", "/gateway/api/publish", "POST", "gateway:publish"),
			apiPerm("回滚配置", "/gateway/api/publish/rollback", "POST", "gateway:publish:rollback"),
			apiPerm("发布历史", "/gateway/api/publish/history", "GET", "gateway:publish:history"),
		),
	),
}
