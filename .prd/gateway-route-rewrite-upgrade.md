# Gateway 单表路由与路由内重写升级方案

## 1. 结论

本次升级采用单表路由模型：

- 不再维护独立的 `gw_rewrite_rule` 表。
- 路由匹配、路由内重写、上游转发统一收敛到 `gw_route`。
- 重写规则属于命中路由的私有配置，不是全局规则。
- 每条重写规则都有自己的匹配条件，路由内可以配置多条条件重写。
- `rewrites` 按 `priority DESC` 排序，相同优先级保持数组顺序。
- 链路固定为：请求进入 Gateway → 路由表达式匹配 → 命中路由执行重写 → 转发上游。
- 重写后的 Path 不参与第二次路由匹配，避免循环和不可预测的路由漂移。

这是一个破坏性升级，不兼容旧的 `gw_endpoint` 路由模型，也不兼容已有的全局重写模型。

## 2. 目标链路

```text
客户端请求
  → Nginx 将客户端 Host、IP、协议等写入 Header 后转发 Gateway
  → 按 priority DESC, id ASC 匹配路由表达式
  → 第一条命中路由胜出
  → 按 priority 顺序检查该路由的 rewrites 规则
  → 命中重写条件后执行对应 actions
  → 遇到 terminal 或 REDIRECT 时终止后续重写
  → 转发到该路由的 upstream
  → 应用响应头策略
  → 返回客户端
```

这里的执行顺序是“先匹配、后重写、再转发”，不是“转发后再重写”。

重写只影响发往上游的请求，不改变客户端地址栏中的 URL。

Gateway 是中介层，不把自身的 `RemoteAddr`、监听地址或网关 Host 当作客户端信息。Nginx 传入的客户端 Header 原样透传给业务服务。

## 3. 数据模型

### 3.1 `gw_route`

`gw_route` 是唯一的业务路由配置表。

```sql
CREATE TABLE `gw_route` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `name` VARCHAR(128) NOT NULL,
  `description` VARCHAR(512) NOT NULL DEFAULT '',
  `enabled` TINYINT(1) NOT NULL DEFAULT 1,
  `priority` INT NOT NULL DEFAULT 0,
  `match_expression` VARCHAR(4096) NOT NULL,
  `rewrites` JSON NULL,
  `upstream` JSON NULL,
  `response` JSON NULL,
  `middlewares` JSON NULL,
  `metadata` JSON NULL,
  `created_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updated_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
    ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  KEY `idx_gw_route_enabled_priority` (`enabled`, `priority`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

字段说明：

| 字段 | 含义 |
| --- | --- |
| `match_expression` | 路由匹配表达式 |
| `rewrites` | 命中路由后执行的条件重写规则数组 |
| `upstream` | 上游协议、地址、超时和重试 |
| `response` | 响应格式和响应头策略 |
| `middlewares` | 路由级中间件 |
| `metadata` | 扩展元数据 |

### 3.2 发布历史

`gw_route_history` 继续保留，用于发布、审计和回滚。

它只保存完整路由快照，不属于第二份业务配置源。

```text
gw_route_history
├── id
├── version
├── snapshot
├── operator
├── remark
└── created_at
```

发布快照只包含 `Routes`，不再包含 `Rewrites`。

## 4. Go 配置模型

```go
type Route struct {
	ID          uint64            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Enabled     bool              `json:"enabled"`
	Priority    int               `json:"priority"`
	Match       Match             `json:"match"`
	Rewrites    []RewriteRule     `json:"rewrites"`
	Upstream    Upstream          `json:"upstream"`
	Response    Response          `json:"response"`
	Middlewares []Middleware      `json:"middlewares"`
	Metadata    map[string]string `json:"metadata"`
}

type RewriteRule struct {
	Name     string   `json:"name"`
	Priority int      `json:"priority"`
	Match    Match    `json:"match"`
	Actions  []Action `json:"actions"`
}

type Match struct {
	Expression string `json:"expression"`
	Terminal   bool   `json:"terminal,omitempty"`
}

type Action struct {
	Type   ActionType `json:"type"`
	Params any        `json:"params"`
}
```

删除：

```text
config.Config.Rewrites
独立重写配置表
全局重写管理接口
```

保留并调整为动作级 API：

```text
rewrite.CompileRules
rewrite.ValidateRules
rewrite.ValidateRule
rewrite.ValidateActions
rewrite.Apply
```

说明：`config.RewriteRule` 继续保留，但只作为 `Route.Rewrites` 的内部元素存在，不再作为独立表或独立管理对象。动作统一使用 `type + params`，`Params` 本身是 `any`，由 `type` 决定读取哪些 key，不再维护与动作类型一一对应的参数结构体。

## 5. 路由示例

### 5.1 蜘蛛首页 SEO

```json
{
  "name": "盘搜蜘蛛首页 SEO",
  "description": "蜘蛛访问首页时转发到 Manager SEO 入口",
  "enabled": true,
  "priority": 1000,
  "match": {
    "expression": "req_header_value_contain(\"User-Agent\", \"spider\", true)"
  },
  "rewrites": [
    {
      "name": "首页 HTML 请求转 SEO 渲染",
      "priority": 100,
      "match": {
        "expression": "req_method_in(\"GET\", \"HEAD\")",
        "terminal": true
      },
      "actions": [
        {
          "type": "PATH_SET",
          "params": {
            "value": "/manager/seo"
          }
        }
      ]
    }
  ],
  "upstream": {
    "protocol": "HTTP",
    "timeoutMs": 10000,
    "backends": [
      {
        "target": "127.0.0.1:7202"
      }
    ]
  },
  "response": {
    "format": false,
    "headers": {}
  }
}
```

### 5.2 普通用户静态站点

```json
{
  "name": "盘搜静态站点",
  "description": "asou.fun / poposo.cc 的普通用户静态站点",
  "enabled": true,
  "priority": -100,
  "match": {
    "expression": "req_host_in(\"asou.fun\", \"www.asou.fun\", \"poposo.cc\", \"www.poposo.cc\")"
  },
  "rewrites": [],
  "upstream": {
    "protocol": "HTTP",
    "timeoutMs": 60000,
    "backends": [
      {
        "target": "127.0.0.1:7402"
      }
    ]
  },
  "response": {
    "format": false,
    "headers": {}
  }
}
```

SEO 路由优先级高于静态站点路由，因此：

- Header `User-Agent` 不区分大小写包含 `spider` 时直接命中 SEO 路由，Path 被改写为 `/manager/seo`。
- 普通用户访问首页时 SEO 路由不命中，继续命中静态站点路由。

蜘蛛判断统一使用请求 Header：

```text
req_header_value_contain("User-Agent", "spider", true)
```

该条件不限制 Host 和 Path：所有 `User-Agent` 包含 `spider` 的请求都会命中此 SEO 路由。需要收窄范围时再追加 `req_host_in(...)` 或 `req_path_in(...)`。

## 6. 条件重写规则

路由内 `rewrites` 是一个条件重写规则数组。每条规则包含名称、优先级、匹配条件和动作列表。规则按 `priority DESC` 执行；相同优先级保持数组顺序。

```json
{
  "name": "规则名称",
  "priority": 100,
  "match": {
    "expression": "req_header_value_prefix_in(\"Accept\", \"text/html\", true)",
    "terminal": true
  },
  "actions": [
    {
      "type": "PATH_SET",
      "params": {
        "value": "/manager/seo"
      }
    }
  ]
}
```

执行规则：

- 路由命中后，按 `priority DESC`、同优先级按数组顺序逐条检查 rewrite 条件。
- 条件基于当前请求状态计算，前一条非 terminal 规则的动作会影响后续条件。
- 条件不命中时直接跳过该规则。
- 条件命中时按顺序执行该规则的 actions。
- `match.terminal=true` 时，执行完该规则后停止后续 rewrite。
- `match.terminal=false` 时，继续检查下一条 rewrite。
- `REDIRECT` 一经执行立即响应客户端，无论 `terminal` 是否为 true。
- 重写规则不能改变已选中的路由和 upstream，只能修改发往该 upstream 的请求。

路由内重写规则示例：

```json
[
  {
    "name": "蜘蛛走 SEO",
    "priority": 100,
    "match": {
      "expression": "req_header_value_contain(\"User-Agent\",\"spider\",true)",
      "terminal": true
    },
    "actions": [
      {
        "type": "PATH_SET",
        "params": {
          "value": "/manager/seo"
        }
      }
    ]
  },
  {
    "name": "活动页改写到 /sale",
    "priority": 50,
    "match": {
      "expression": "req_query_value_in(\"from\",\"ad\")",
      "terminal": true
    },
    "actions": [
      {
        "type": "PATH_PREFIX_ADD",
        "params": {
          "prefix": "/sale"
        }
      }
    ]
  }
]
```

## 7. 重写动作

动作按当前 rewrite 规则的 `actions` 数组顺序执行，所有参数统一放在 `params` 中。

| 类型 | 参数 | 作用 |
| --- | --- | --- |
| `HOST_SET` | `value` | 设置上游请求 Host |
| `PATH_SET` | `value` | 直接设置 Path |
| `PATH_PREFIX_ADD` | `prefix` | 添加 Path 前缀 |
| `PATH_PREFIX_TRIM` | `prefix` | 截断 Path 前缀 |
| `PATH_REGEX_REPLACE` | `pattern`、`replacement` | 正则替换 Path |
| `RAW_QUERY_ADD` | `key`、`value` | 添加 Query |
| `RAW_QUERY_RENAME` | `key`、`newKey` | 重命名 Query |
| `RAW_QUERY_DEL` | `key` | 删除 Query |
| `RAW_QUERY_DEL_ALL_EXCEPT` | `keys` | 只保留指定 Query |
| `RAW_QUERY_CLEAR` | 无 | 清空 Query |
| `REQUEST_HEADER_SET` | `key`、`value` | 设置请求头 |
| `REQUEST_HEADER_ADD` | `key`、`value` | 追加请求头 |
| `REQUEST_HEADER_DEL` | `key` | 删除请求头 |
| `RESPONSE_HEADER_SET` | `key`、`value` | 设置响应头 |
| `RESPONSE_HEADER_ADD` | `key`、`value` | 追加响应头 |
| `RESPONSE_HEADER_DEL` | `key` | 删除响应头 |
| `REDIRECT` | `statusCode`、`location`、`preserveQuery` | 直接重定向 |

执行规则：

- `REDIRECT` 一旦执行，立即停止后续动作并返回客户端。
- `REDIRECT` 必须位于动作数组最后，否则发布校验失败。
- 重写规则条件不命中时，不执行该规则中的任何动作。
- 重写规则条件基于当前请求，前序非 terminal 规则可能影响后续规则的条件判断。
- 重定向响应同样应用此前声明的响应头动作。
- 没有 `REDIRECT` 的动作只修改发往上游的请求。

## 8. 客户端 Header 透传

业务服务关心的是客户端信息，不是 Gateway 自身信息。Gateway 不重新生成 `X-Forwarded-*`，也不用自己的连接信息覆盖 Nginx 传来的值。

透传规则：

- `X-Forwarded-For`：原样使用 Nginx 传入的客户端 IP 链，不追加 Gateway 或 Nginx 的地址。
- `X-Forwarded-Host`：原样使用 Nginx 传入的原始客户端 Host。
- `X-Forwarded-Proto`：原样使用 Nginx 传入的客户端协议。
- `X-Forwarded-Port`：原样使用 Nginx 传入的客户端端口。
- `Host`：按 Nginx 传入值透传，除非路由显式配置 `HOST_SET`。
- 缺失时保持缺失，不由 Gateway 猜测或补造。

因此 Manager SEO 直接读取 Nginx 传入的 `X-Forwarded-Host`，不依赖 Gateway 计算。

安全前提：

- 7200 端口只允许 Nginx 或内网访问，不能直接暴露公网。
- Nginx 负责清理不可信的入站 `X-Forwarded-*`，再写入真实客户端信息。

Nginx 到 Gateway 至少应显式设置：

```nginx
proxy_set_header Host $host;
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
proxy_set_header X-Forwarded-Host $host;
proxy_set_header X-Forwarded-Proto $scheme;
```

删除：

```text
config.Forward.Forwarded
forward.TrustIncoming
Gateway 侧 X-Forwarded-* 注入逻辑
Gateway 侧 X-Forwarded-For 追加逻辑
```

`REQUEST_HEADER_SET / ADD / DEL` 仍保留在 rewrites 中，只有路由显式配置时才修改请求头。

## 9. 管理 API

唯一路由接口：

```text
GET    /gateway/api/routes
POST   /gateway/api/routes
PUT    /gateway/api/routes?id=
DELETE /gateway/api/routes?id=
```

删除接口：

```text
GET    /gateway/api/rewrites
POST   /gateway/api/rewrites
PUT    /gateway/api/rewrites
DELETE /gateway/api/rewrites
```

保留发布接口：

```text
POST /gateway/api/publish
POST /gateway/api/publish/rollback
GET  /gateway/api/publish/history
```

权限收敛为：

```text
gateway:route:list
gateway:route:add
gateway:route:update
gateway:route:delete
gateway:publish
```

删除权限：

```text
gateway:rewrite:list
gateway:rewrite:add
gateway:rewrite:update
gateway:rewrite:delete
```

## 10. 发布与回滚

发布流程：

1. 读取全部 `gw_route`。
2. 全量校验表达式、动作、上游和响应头策略。
3. 写入新版本 `gw_route_history`。
4. 写入 Redis 快照。
5. 通过 Pub/Sub 通知所有 Gateway 实例回源拉取。

回滚流程：

1. 读取指定版本快照。
2. 全量替换 `gw_route`。
3. 生成新的版本号。
4. 写入 Redis 快照并通知所有实例。

Redis 配置键升级为：

```text
gw:config:snapshot:v4
gw:config:changed:v4
```

不使用 `v3`，避免实例误读旧的“全局重写规则”快照。

## 11. 校验规则

路由校验：

- 名称不能为空。
- 匹配表达式不能为空且必须能编译。
- `priority + expression` 不允许重复。
- 没有 `REDIRECT` 时必须配置合法的上游。
- 有 `REDIRECT` 时允许不配置上游。
- 上游协议只允许 `HTTP` 和 `GRPC`。
- 上游后端地址不能为空。

重写规则校验：

- 规则名称不能为空。
- rewrite 的 `priority` 没有范围限制，但同一条路由内建议保持可读的优先级梯度。
- 每条 rewrite 的 `match.expression` 不能为空且必须能编译。
- 每条 rewrite 至少包含一个 action。
- 多条 rewrite 允许使用相同条件，但建议在 UI 中提示可能重复执行。
- `match.terminal` 作用于规则命中后的后续 rewrite 检查，不作用于 actions 内部。

动作校验：

- 动作类型必须在白名单内。
- Path 类参数必须以 `/` 开头。
- 正则必须能编译。
- Header 名称和值不能包含 CR/LF。
- Query 键不能为空。
- 重定向状态码只允许 `301`、`302`、`303`、`307`、`308`。
- 重定向地址只允许 `/`、`http://`、`https://` 开头。
- `REDIRECT` 必须是最后一个动作。

## 12. 前端设计

页面统一为一个“网关路由”管理页，不再拆成“路由”和“重写规则”两个 Tab。

列表字段：

- 名称
- 优先级
- 匹配表达式
- 上游地址或重定向目标
- 重写动作数量
- 启用状态
- 更新时间
- 操作

编辑表单分区：

1. 基本信息：名称、描述、优先级、启用状态。
2. 匹配条件：表达式输入框和函数提示。
3. 路由内重写：`rewrites` 条件规则动态列表，每条规则单独编辑名称、priority、条件、terminal 和 actions。
4. 上游服务：协议、超时、后端地址、重试。
5. 高级配置：响应头、中间件、元数据。

条件重写编辑器：

- 支持新增、删除、上移、下移 rewrite 规则。
- 每条 rewrite 独立配置名称、priority、条件表达式、`match.terminal` 和动作列表。
- 动作按类型动态展示参数。
- 当选择 `REDIRECT` 时，自动移动到该规则动作列表末尾并禁用后续新增。
- 列表摘要展示路由表达式、rewrite 规则数量和动作总数。

## 13. 数据库迁移

更新现有迁移：

```text
server/gateway/deploy/migrations/20261109_gateway_expression_routes.sql
```

迁移内容：

1. 创建或升级 `gw_route`，增加 `rewrites JSON NULL`。
2. 不再创建 `gw_rewrite_rule`。
3. 如已存在 `gw_rewrite_rule`，删除该表。
4. 保留 `gw_route_history`。
5. 种子数据直接写入 SEO 路由及其条件 rewrite：`GET/HEAD` → `PATH_SET /manager/seo`，`terminal=true`。
6. 同步更新 `deploy/platform/sql`。

旧的 `gw_endpoint` 和 `gw_endpoint_history` 不再参与运行流程。

## 14. 测试与验收

单元测试：

- 表达式解析和函数匹配。
- 路由内只有条件命中的 rewrite 规则会执行。
- `match.terminal=true` 停止后续 rewrite，`match.terminal=false` 继续执行。
- `priority DESC` 决定 rewrite 执行顺序，同优先级保持数组顺序。
- 前序非 terminal 规则的动作会影响后续规则的条件。
- 动作参数校验。
- `REDIRECT` 必须位于末尾。
- Path、Query、Host、请求头重写。
- 重定向响应头。
- `gw_route` 与 `Route` 的 JSON 往返。

集成测试：

- 路由未命中时不执行任何重写。
- 路由命中后才执行该路由的重写动作。
- 重写后的 Path、Query、Host 正确传到上游。
- `X-Forwarded-*` 由 Nginx 传入并原样到达上游，Gateway 不重算、不追加。
- SEO 路由命中 Manager。
- 普通用户命中 web-server。
- 重定向不访问上游。

本地验收：

```text
Host: asou.fun
User-Agent: Baiduspider
GET /

期望：Manager 返回 SEO HTML
```

```text
Host: asou.fun
User-Agent: Mozilla/5.0
GET /

期望：web-server 返回静态 SPA
```

```text
Host: asou.fun
GET /some/spa/path

期望：web-server 返回静态 SPA
```

## 15. 非目标

本次不做：

- 全局重写规则。
- 重写后的二次路由匹配。
- 多路由规则合并。
- 独立重定向规则表。
- 旧 `/gateway/api/endpoints` 和 `/gateway/api/rewrites` 的兼容层。

需要统一重写时，使用一条高优先级 catch-all 路由。

## 16. 实施顺序

1. 修改配置模型和数据库存储，合并为单表路由。
2. 将重写执行器改为路由内条件规则执行。
3. 调整 Proxy 执行链为“匹配 → 重写 → 转发”。
4. 调整管理 API、权限和发布快照。
5. 更新迁移种子数据和部署 SQL。
6. 重写前端路由管理页。
7. 补齐单元测试和集成测试。
8. 本地 7200 端到端验证。
