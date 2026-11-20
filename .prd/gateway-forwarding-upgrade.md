# Gateway 转发能力升级方案

## 结论

可以支持 Path 重写和 302/301 重定向，但不再兼容旧的配置写法。

当前网关已经能通过 `rewrite` 中间件处理 `pathRewrite`、`stripPrefix` 和 `hostRewrite`，但没有原生 302 重定向能力。此次升级把这类能力从中间件参数提升为路由的一等配置，并删除旧的 `rewrite` 中间件。

升级后每个路由必须明确是：

- `proxy`：转发到上游，可重写 Host、Path、请求头和响应头。
- `redirect`：不访问上游，直接返回 301/302/303/307/308。

## 一、配置模型

```go
type Action string

const (
	ActionProxy    Action = "proxy"
	ActionRedirect Action = "redirect"
)

type Endpoint struct {
	ID          uint64            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Enabled     bool              `json:"enabled"`
	Priority    int               `json:"priority"`
	Action      Action            `json:"action"`
	Match       Match             `json:"match"`
	Upstream    Upstream          `json:"upstream"`
	Forward     Forward           `json:"forward"`
	Response    Response          `json:"response"`
	Redirect    *Redirect         `json:"redirect,omitempty"`
	Middlewares []Middleware      `json:"middlewares"`
	Metadata    map[string]string `json:"metadata"`
}

type Match struct {
	Host   string `json:"host"`
	Path   string `json:"path"`
	Method string `json:"method"`
}

type Upstream struct {
	Protocol  string    `json:"protocol"`
	TimeoutMs int64     `json:"timeoutMs"`
	Backends  []Backend `json:"backends"`
	Retry     *Retry    `json:"retry,omitempty"`
}

type Forward struct {
	Host              string         `json:"host"`
	Path              PathRewrite    `json:"path"`
	RequestHeaders    HeaderPolicy   `json:"requestHeaders"`
	Forwarded         Forwarded      `json:"forwarded"`
}

type PathRewrite struct {
	StripPrefix   string `json:"stripPrefix"`
	Pattern       string `json:"pattern"`
	Replacement   string `json:"replacement"`
	PreserveQuery bool   `json:"preserveQuery"`
}

type HeaderPolicy struct {
	Set    map[string]string `json:"set"`
	Add    map[string]string `json:"add"`
	Remove []string          `json:"remove"`
}

type Forwarded struct {
	Enable        bool `json:"enable"`
	TrustIncoming bool `json:"trustIncoming"`
}

type Response struct {
	Format  bool         `json:"format"`
	Headers HeaderPolicy `json:"headers"`
}

type Redirect struct {
	StatusCode    int    `json:"statusCode"`
	Location      string `json:"location"`
	PreserveQuery bool   `json:"preserveQuery"`
}
```

## 二、路由匹配与优先级

匹配字段统一收口到 `match`：

```json
{
  "match": {
    "host": "go.example.com",
    "path": "/api/*",
    "method": "GET"
  }
}
```

规则：

- `match.host` 为空表示任意 Host。
- `match.path` 必须以 `/` 开头，支持精确路径、`{name}` 参数和末尾 `*` 前缀匹配。
- `match.method` 为空或 `*` 表示任意方法。
- 方法为 `GET` 时自动接受 `HEAD`。
- 排序依次比较：`priority DESC`、Host 是否精确、Path 是否精确、Path 长度 `DESC`、ID `ASC`。
- 同一请求命中多条路由时，使用排序后的第一条，不做规则合并。

## 三、Path 重写

只有 `action=proxy` 时启用 `forward.path`。执行顺序固定为：

1. 根据 `stripPrefix` 截掉前缀。
2. 使用 `pattern` 和 `replacement` 做正则替换。
3. 根据 `preserveQuery` 决定是否保留原 query。

示例：把 `/old-api/users/42` 转发到上游 `/v2/users/42`：

```json
{
  "action": "proxy",
  "match": {
    "path": "/old-api/*",
    "method": "GET"
  },
  "upstream": {
    "protocol": "HTTP",
    "timeoutMs": 5000,
    "backends": [
      {"target": "http://127.0.0.1:7202"}
    ]
  },
  "forward": {
    "host": "internal.example.com",
    "path": {
      "stripPrefix": "/old-api",
      "pattern": "^/(.*)$",
      "replacement": "/v2/$1",
      "preserveQuery": true
    }
  }
}
```

校验规则：

- `stripPrefix` 必须为空或以 `/` 开头。
- `pattern` 必须能被 `regexp.Compile` 编译。
- 只允许同时配置 `pattern` 和 `replacement`。
- 重写结果不能为空，并且必须以 `/` 开头。
- `preserveQuery=false` 时清空原 query；默认值为 `true`，反序列化后由校验逻辑补默认值。
- 重写只改变转发到上游的路径，不改变客户端看到的 URL。

`forward.host` 规则：

- 为空：保留客户端原始 Host。
- 非空：把上游 `req.Host` 改成该值。
- 不再保留 `hostMode`、`hostRewrite` 两套表达。

## 四、302/301 重定向

`action=redirect` 时不创建上游 client、不访问任何后端，直接返回重定向响应。

示例：HTTP 到 HTTPS，或旧域名统一跳新域名：

```json
{
  "action": "redirect",
  "match": {
    "host": "old.example.com",
    "path": "/*",
    "method": "*"
  },
  "redirect": {
    "statusCode": 302,
    "location": "https://${request.hostWithPort}${request.path}",
    "preserveQuery": true
  }
}
```

`statusCode` 只允许：

- `301`：永久重定向，SEO 域名迁移优先使用。
- `302`：临时重定向。
- `303`：See Other。
- `307`：临时重定向且保持请求方法。
- `308`：永久重定向且保持请求方法。

`location` 支持以下变量：

- `${request.scheme}`：`http` 或 `https`。
- `${request.host}`：不带端口的 Host。
- `${request.hostWithPort}`：客户端原始 Host，包含端口。
- `${request.path}`：原始请求路径。
- `${request.query}`：不带 `?` 的原始 query。
- `${endpoint.host}`：当前路由的 `match.host`。
- `${endpoint.path}`：当前路由的 `match.path`。

安全校验：

- `location` 只允许以 `/`、`http://`、`https://` 开头。
- 禁止 `javascript:`、`data:`、`file:` 等协议。
- `preserveQuery=true` 且 `location` 中没有 `${request.query}` 时，自动追加原 query。
- 如果 `location` 中已经显式使用了 `${request.query}`，不再重复追加。
- 响应头 `Location` 必须是绝对 URL，或以 `/` 开头的站内路径。

## 五、Host、Header 与 Forwarded

请求头策略：

```json
{
  "forward": {
    "requestHeaders": {
      "set": {"X-Env": "prod"},
      "add": {"X-Tag": "gateway"},
      "remove": ["X-Internal-Token"]
    }
  }
}
```

响应头策略：

```json
{
  "response": {
    "format": true,
    "headers": {
      "set": {"Cache-Control": "no-store"},
      "remove": ["Server"]
    }
  }
}
```

`set` 使用 `Header.Set`，`add` 使用 `Header.Add`，`remove` 使用 `Header.Del`。Header 名称写入前统一 canonicalize，拒绝包含换行符的名称和值。

`forward.forwarded` 统一控制代理相关请求头：

- `X-Forwarded-Host`
- `X-Forwarded-Proto`
- `X-Forwarded-Port`
- `X-Forwarded-For`
- `X-Request-ID`（缺失时生成，存在时保留）

`trustIncoming=false` 时覆盖客户端伪造的 Forwarded 头；为 `true` 时只追加可信链路。默认 `false`。不再为每个 Header 单独保留开关。

## 六、旧字段处理

不做兼容，直接删除或迁移：

| 旧字段 | 新字段 |
|---|---|
| `title` | `name` |
| `path` | `match.path` |
| `method` | `match.method` |
| `host` | `match.host` |
| `protocol` | `upstream.protocol` |
| `timeoutMs` | `upstream.timeoutMs` |
| `backends` | `upstream.backends` |
| `retry` | `upstream.retry` |
| `responseFormat` | `response.format` |
| `rewrite` 中间件 | `forward.path`、`forward.host`、`forward.requestHeaders`、`response.headers` |
| `hostMode` | 删除；由 `forward.host` 是否为空决定 |

同时删除 `config.Rewrite`、`config.RewriteHeadersPolicy` 和整个 `middleware/rewrite` 包，以及注册表中的 `rewrite`。

## 七、数据库结构

`gw_endpoint` 直接重建。旧表改名为 `gw_endpoint_legacy`，一次性迁移脚本只做字段映射，不兼容旧配置语法。

```sql
CREATE TABLE `gw_endpoint` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `name` varchar(128) NOT NULL,
  `description` varchar(512) NOT NULL DEFAULT '',
  `enabled` tinyint(1) NOT NULL DEFAULT 1,
  `priority` int NOT NULL DEFAULT 0,
  `action` varchar(16) NOT NULL,
  `match_host` varchar(191) NOT NULL DEFAULT '',
  `match_path` varchar(512) NOT NULL,
  `match_method` varchar(32) NOT NULL DEFAULT '',
  `upstream` json DEFAULT NULL,
  `forward` json DEFAULT NULL,
  `redirect` json DEFAULT NULL,
  `response` json DEFAULT NULL,
  `middlewares` json DEFAULT NULL,
  `metadata` json DEFAULT NULL,
  `created_at` datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updated_at` datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  KEY `idx_match` (`match_host`, `match_path`),
  KEY `idx_enabled_priority` (`enabled`, `priority`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

删除列：

- `title`
- `path`
- `method`
- `host`
- `protocol`
- `response_format`
- `timeout_ms`
- `backends`
- `retry`

`gw_endpoint_history.snapshot` 的旧结构不兼容。升级前归档历史表，升级后从新配置重新发布第一版基线。

## 八、运行时改造

### 8.1 构建路由

`proxy.buildEndpoint` 按 `Action` 分流：

```text
redirect:
  校验 Redirect 配置
  构造不访问上游的 http.Handler

proxy:
  clientFactory(upstream)
  forward(host/path/header)
  路由级 middlewares
  全局 middlewares
  retry/timeout
```

`redirect` 分支禁止配置 `upstream.backends`，避免出现“写了后端但永远不访问”的歧义。`proxy` 分支禁止配置 `redirect`。

### 8.2 转发层

新增 `proxy/forward` 包，职责只包括：

- 改写 `req.Host`。
- 改写 `req.URL.Path` 和 query。
- 设置请求头。
- 接收响应后设置响应头。
- 注入 Forwarded 头和请求 ID。

删除 `middleware/rewrite`，避免同一能力存在两套入口。业务中间件仍按原顺序包装 RoundTripper。

### 8.3 路由层

`router.Router.Handle` 改为接收完整 `config.Match` 或路由对象。`mux` 实现不再分别接收 `pattern/method/host`，并在 `Update` 前按统一优先级排序。

`GET` 路由自动注册 `HEAD`；显式配置 `HEAD` 时不再重复注册。

## 九、管理 API 与前端

管理 API 的 DTO 改为与领域模型同构的五块：

1. 基本信息：`name`、`description`、`enabled`、`priority`。
2. 匹配条件：`match`。
3. 动作：`action`。
4. 上游与转发：`upstream`、`forward`、`response`。
5. 重定向：`redirect`。

表单根据 `action` 切换：

- `proxy` 显示上游、Path 重写、Header、超时、重试。
- `redirect` 显示状态码、Location、是否保留 query。

发布前新增全量校验：

- 路由名称非空。
- `action` 合法。
- 精确匹配的 Host + Path + Method 不允许重复。
- `proxy` 必须有可用后端；`redirect` 必须有合法 Location。
- 正则、Header 名称、状态码和 Path 前缀全部校验通过。

## 十、迁移与上线

### 10.1 升级前

1. 备份 `gw_endpoint` 和 `gw_endpoint_history`。
2. 将旧表改名为 `gw_endpoint_legacy`。
3. 创建新表。
4. 记录当前 7 条线上路由作为回滚依据。

### 10.2 重新灌入线上路由

新表建立后重新录入：

| Path | 上游 | 中间件 |
|---|---|---|
| `/manager/*` | `http://127.0.0.1:7202` | 无 |
| `/resource/api/*` | `http://127.0.0.1:7203` | auth |
| `/configure/api/*` | `http://127.0.0.1:7201` | 无 |
| `/cron/api/*` | `http://127.0.0.1:7204` | auth |
| `/ai-agent/api/*` | `http://127.0.0.1:7206` | auth |
| `/psou/api/*` | `http://127.0.0.1:7301` | 无 |
| `/psou/admin/*` | `http://127.0.0.1:7301` | auth |

发布基线后，再增加 Host 路由、SEO 页面和重定向。`poposo.cc`、`asou.fun`、`pre.psou.us` 当前直连 `7402`，后续统一改为经过 Gateway。

### 10.3 回滚

- 配置错误：回滚到升级后发布的第一版快照。
- 代码错误：回退 Gateway 镜像，同时恢复 `gw_endpoint_legacy`。
- 数据库错误：停写管理 API，恢复备份，重新发布快照并通知所有实例。

## 十一、实施阶段

### Phase 1：配置与存储

- 重构 `config/types.go`。
- 重建 `gw_endpoint` 表。
- 改造 `config/store.go` 的 JSON 列读写。
- 改造 `admin/dto.go`、`admin/validate.go`。

### Phase 2：转发能力

- 新增 `proxy/forward`。
- 删除 `middleware/rewrite` 及注册。
- 实现 Host 改写、Path 重写、Header 策略和 Forwarded。

### Phase 3：动作与路由

- 实现 `redirect` Handler。
- `proxy.buildEndpoint` 按 `action` 分支。
- 改造 `router.Handle` 和路由优先级排序。
- 支持 `GET` 自动匹配 `HEAD`。

### Phase 4：管理与上线

- 更新管理前端表单。
- 重新灌入线上 7 条路由。
- 发布并验证代理、Path 重写、302、Host 和 Header 场景。

## 十二、验收用例

1. `/old-api/users/42` 经 `stripPrefix` 和正则重写后到上游 `/v2/users/42`。
2. `http://old.example.com/a?x=1` 返回 302，并保留 query。
3. `old.example.com` 的 301 跳转不被客户端缓存污染新域名。
4. `forward.host` 为空时上游收到原始 Host，非空时收到指定 Host。
5. 请求头和响应头的 `set/add/remove` 均生效。
6. Host 精确路由优先于通配路由，长 Path 优先于短 Path。
7. `GET` 路由自动接受 `HEAD`，且 `HEAD` 不返回 body。
8. 非法正则、非法 Location、重复精确路由在发布阶段被拒绝。
9. `action=redirect` 不创建上游连接，上游宕机不影响跳转。
10. 旧 `rewrite` 中间件无法被引用，引用后发布校验直接报错。
