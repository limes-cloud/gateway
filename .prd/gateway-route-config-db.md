# gateway 网关配置存储重构：配置中心 → MySQL

> 路由字段已升级为 `action/match/upstream/forward/response` 新模型，当前权威设计见
> `.prd/gateway-forwarding-upgrade.md`。本文仅保留存储和发布机制的背景说明。

> 本文定义网关路由配置从配置中心（limes-cloud/configure）迁移到 MySQL 的整体模型与实施路径，是后续编码（`config` 包 Loader 重构、gateway 管理 API、菜单自注册、迁移脚本）的依据。
> 先读本文确定方向，再按「迁移方案」逐条落地。
>
> 设计约束：**表建在 gateway 自有的数据库（MySQL），管理功能通过自注册菜单挂到 manager（复用 `RegisterMenu` 机制），manager 侧零代码改动**。

## 背景

### 现状：配置流与启动依赖

网关当前配置来源在 `main.go:59-70` **二选一**：

```go
func configSource() kc.Source {
	env.Load()
	host := os.Getenv("CONF_HOST")
	token := os.Getenv("CONF_TOKEN")
	name := os.Getenv("APP_NAME")
	if host != "" && token != "" && name != "" {
		return configure.New(host, name, token) // 配置中心
	}
	return file.NewSource("config/config.yaml") // 本地文件
}
```

配置内容（`config/config.yaml`）分两类：

| 类 | 内容 | 实例数 |
|---|---|---|
| 实例级 | `debug` / `addr` / `name` / `version` | 1 组 |
| 路由级 | `endpoints[]`（path/method/protocol/backends/middlewares/timeout/retry...） | 6 条 |
| 全局 | `middlewares[]`（bbr/cors/tracing/logging/transcoder） | 5 个 |

### 配置中心的模型与问题

`configure` 的 client（`configure/api/configure/client/config.go:25-36`、`watcher.go`）是 **app 级整体内容模型**：

- 通过 gRPC 双向流 `WatchConfigure(app, token)` 拉取，服务端推送**一个完整字符串**（`cf.Content`），`debug/addr/endpoints/middlewares` 全在里面；
- 变更推送为**全量**而非 diff，无 schema 校验、无编辑界面、无权限、无审计。

由此产生四个问题：

1. **启动强依赖**：`client/config.go:28` `DialInsecure` 失败直接 `panic`；`main.go:42` 加载失败 `log.Fatal` —— **configure 不可达，网关（全系统流量入口）无法启动**，依赖方向是反的。
2. **无管理入口**：路由增多后只能在 configure 平台编辑整个 YAML 长串，无法逐条增删改查，无法挂权限/审计。
3. **无版本与回滚**：覆盖即生效，改错一个字段（如 `paths` 拼错）线上全断，无回滚手段。
4. **与业务管理后台割裂**：路由配置无法进入 manager 的菜单/权限体系。

### 决策

**用 MySQL 作为网关路由配置的事实源（管理面），彻底移除 `limes-cloud/configure` 依赖；表与管理 API 归 gateway 自己所有（单一职责），通过自注册菜单挂入 manager；网关运行期不依赖任何外部组件即可启动（降级链），变更通过 Redis PubSub 通知 + 回源拉取实现热更新。**

选型依据：

- 路由表是**结构化、需要 CRUD/权限/审计**的数据（对应 Kong 的 DB 模式），而非"环境相关的整体配置文件"；本项目技术栈为 MySQL（见 README「技术选型」与 manager 建表脚本风格）。
- **表归 gateway、管理入口注册到 manager**，与 resource / configure 服务的模式一致：它们各自维护业务表，通过 `RegisterMenu`（`manager/api/menu/proto/menu.proto`）在启动时向 manager 注册菜单与接口权限（`app` 标识提供方，如 `resource`、`configure`）。manager 只做统一展示与鉴权入口，**不需要为网关改任何代码**。
- 多容器部署下，配置变更需要广播到所有实例（Redis PubSub 已是既有组件，零新增）。

---

## 一、总体架构

```
                     管理面（可复杂，gateway 自持）
┌───────────┐  ① 登录 manager 后访问   ┌──────────────────────────────────────┐
│  管理后台   │ ───────────────────────→ │  gateway 管理 API（/gateway/api/*）   │
│ (admin-web)│     经网关路由 + 鉴权     │  校验 → 事务写库 → 快照 → 通知          │
└───────────┘                          └──────────────┬───────────────────────┘
                                                      │ ② 写
                                                      ▼
                                        ┌──────────────────────────┐
                                        │  MySQL（gateway 自有库）    │ ← 事实源：gw_endpoint / gw_endpoint_history
                                        └────────────┬─────────────┘
                                                     │ ③ 全量快照
                                                     ▼
                                        ┌──────────────────────────┐
                                        │  Redis                    │ ← 共享快照 gw:config:snapshot（多实例一致）
                                        │                           │ ← 通知通道 gw:config:changed（仅版本号，不含内容）
                                        └────────────┬─────────────┘
                                                     │ ④ PubSub 通知
                        ┌────────────────────────────┼────────────────────────────┐
                        ▼                            ▼                            ▼
                 ┌────────────┐               ┌────────────┐               ┌────────────┐
                 │ gateway-A  │               │ gateway-B  │               │ gateway-C  │  ← 多容器，各自订阅
                 └────────────┘               └────────────┘               └────────────┘
                         运行时（必须简单）：启动拉一次 → 常驻内存 → 订阅 → 原子替换

       ⑤ 菜单/权限注册（启动时一次）：gateway ──RegisterMenu(app="gateway")──→ manager（零改动，仅登记）
```

**核心原则：管理面/运行面分离 + 单一归属。**

| 面 | 依赖 | 职责 | 故障语义 |
|---|---|---|---|
| 管理面 | gateway 自有 MySQL + Redis | 编辑、校验、发布、版本、审计 | 挂 = 配置不可变更，**不影响存量流量** |
| 运行面 | 内存 + 启动降级链 | 服务流量、热更路由 | 任何组件挂 = 配置旧一点，**服务不掉** |
| manager | 仅登记 | 菜单入口、角色权限、鉴权 | 挂 = 管理界面不可用，运行不受影响 |

---

## 二、数据库设计（gateway 自有库，MySQL）

### 2.1 `gw_endpoint` 路由表

字段与现有 `config/types.go` 的 `Endpoint` / `Middleware` / `Backend` / `Retry` 一一对应，嵌套对象存 JSON：

```sql
CREATE TABLE IF NOT EXISTS gw_endpoint (
    id               BIGINT       NOT NULL AUTO_INCREMENT COMMENT '主键',
    path             VARCHAR(512) NOT NULL COMMENT '路由匹配路径',
    method           VARCHAR(32)  NOT NULL DEFAULT ''  COMMENT 'HTTP 方法',
    host             VARCHAR(64)  NOT NULL DEFAULT ''  COMMENT 'Host 头匹配',
    protocol         VARCHAR(16)  NOT NULL DEFAULT 'HTTP' COMMENT '协议类型',
    response_format  TINYINT(1)   NOT NULL DEFAULT 0   COMMENT '是否格式化响应',
    timeout_ms       BIGINT       NOT NULL DEFAULT 0   COMMENT '请求超时（毫秒）',
    description      VARCHAR(255) NOT NULL DEFAULT ''  COMMENT '备注',
    metadata         JSON         NULL                 COMMENT '元数据',
    backends         JSON         NOT NULL             COMMENT '后端列表',
    middlewares      JSON         NULL                 COMMENT '中间件列表',
    retry            JSON         NULL                 COMMENT '重试策略',
    enabled          TINYINT(1)   NOT NULL DEFAULT 1   COMMENT '启用状态',
    created_at       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uk_gw_endpoint (host, path, method)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COMMENT = '网关路由配置表';
```

字段说明：

| 字段 | 说明 |
|---|---|
| `id` | 主键，自增 |
| `path` | 路由匹配路径，如 `/manager/api/v1/*`。`*` 后缀表示前缀匹配（`/manager/api/v1/*` 命中 `/manager/api/v1/` 下所有路径，与 mux 的 `PathPrefix` 语义一致） |
| `method` | HTTP 方法（`GET`/`POST`/`PUT`/`DELETE`/...），空 = 不限制；仅配置 `*` 时等价于空；网关会对命中路由自动附加 `OPTIONS` 处理 CORS 预检 |
| `host` | 请求 `Host` 头匹配，空 = 任意 |
| `protocol` | `HTTP` 或 `GRPC`。`GRPC` 走 h2c 直连 + transcoder 中间件做 protobuf/JSON 转换 |
| `response_format` | `1` = 响应统一包装为 `{code, reason, data, message, traceId}`（见 `proxy/response.go`）；`0` = 原样透传 |
| `timeout_ms` | 请求总超时（毫秒），`0` = 不超时；未配置 `retry.timeout_ms` 时同时作为重试每轮超时兜底 |
| `description` | 备注，无逻辑含义 |
| `metadata` | 原 `Endpoint.Metadata`，当前供 metrics 标签 `service` / `basePath` 使用；`NULL` 按空对象处理 |
| `backends` | 后端列表，格式 `[{"target":"127.0.0.1:7001","weight":10}]`。`target` 支持直接地址或服务发现（`discovery://服务名`）；`weight` 为调度权重，空或 <=0 时取默认值 10（见 `client/factory.go` `nodeWeight`） |
| `middlewares` | 该路由专属中间件列表，格式 `[{"name":"auth","options":{...},"required":false}]`。`name` 必须是已注册的中间件（bbr/cors/tracing/logging/transcoder/auth/signature/rewrite/circuitbreaker）；`options` 为中间件选项（见各中间件定义）；`required=true` 时创建失败将导致该路由不可用；`NULL` = 无 |
| `retry` | 重试策略，格式 `{"count":2,"timeout_ms":1000,"conditions":[{"statusCode":"500-599"}]}`。`count` 为总尝试次数（含首次），`timeout_ms` 为每轮超时，`conditions` 为命中重试的条件（`statusCode` 支持单值或 `"500-599"` 范围，或按响应头 `header` 判断，见 `proxy/condition`）；`NULL` = 不重试 |
| `enabled` | `1` = 下发生效；`0` = 软禁用（保留在表内，不下发、不参与快照） |
| `created_at` / `updated_at` | 创建/更新时间（毫秒精度，DB 自动维护） |
| `uk_gw_endpoint(host, path, method)` | 唯一键：同一 `host` 下路由不可重复，重复插入报错 |

> 注：`metadata` / `middlewares` / `retry` 为 JSON 列，MySQL 不支持 `DEFAULT` 字面量故允许 `NULL`，应用层读取时以空对象/空数组兜底；查询与写入均通过 gorm（与 kratosx 生态一致）。

### 2.2 `gw_endpoint_history` 发布历史表

发布制（见第四节）的版本与回滚依据：

```sql
CREATE TABLE IF NOT EXISTS gw_endpoint_history (
    id         BIGINT       NOT NULL AUTO_INCREMENT COMMENT '主键',
    version    BIGINT       NOT NULL COMMENT '发布版本号',
    snapshot   JSON         NOT NULL COMMENT '全量下发配置快照',
    operator   VARCHAR(64)  NOT NULL COMMENT '发布人',
    remark     VARCHAR(255) NOT NULL DEFAULT '' COMMENT '发布说明',
    created_at DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uk_gw_endpoint_history_version (version)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COMMENT = '网关路由发布历史表';
```

| 字段 | 说明 |
|---|---|
| `id` | 主键，自增 |
| `version` | 发布版本号，发布时分配并保证全局唯一（与 `gw:config:changed` 通知中的版本号一致），回滚时按此定位 |
| `snapshot` | 该版本发布时的**全量下发配置**（与 `config.Config` 同构：endpoints + 全局 middlewares），回滚 = 取某版本快照重新发布 |
| `operator` | 发布人用户名（取自登录态），审计用 |
| `remark` | 发布说明，回滚确认时展示 |
| `created_at` | 发布时间（毫秒精度） |

### 2.3 配置 → 列映射

| `config.yaml` 现有键 | 归属 | 去向 |
|---|---|---|
| `debug` / `addr` / `name` / `version` | 实例级 | **留在 `config/config.yaml`**（启动引导，见 3.4） |
| `middlewares[]`（全局） | 实例级 | **留在 `config/config.yaml`**（每个 endpoint 的中间件在表内） |
| `endpoints[]` | 路由级 | **迁入 gateway 库 `gw_endpoint`**，每条一行 |
| `endpoints[].middlewares` | 路由级 | `middlewares` JSON |
| `endpoints[].backends` / `.retry` / `.metadata` / `.timeout` | 路由级 | 对应列 / JSON |

---

## 三、网关改造

### 3.1 Loader 接口（`config` 包重构）

现 `config.New(source kc.Source)`（`config/config.go:21-31`）仅支持单一 kratos Source；重构为 Loader 抽象，实现降级链：

```go
// Loader 配置加载器：启动引导 + 运行时更新
type Loader interface {
    // Load 启动时加载：按降级链返回一份配置（任何情况都不应失败，除非连文件都没有）
    Load(ctx context.Context) (*Config, error)
    // Watch 注册变更回调（内部：PubSub 订阅 + 周期对账），回调在独立 goroutine 中触发
    Watch(ctx context.Context, fn func(*Config))
    // Close 释放订阅与连接
    Close() error
}

// NewLoader 组装降级链：file(保底) → redisSnapshot(次新) → db(最新)
func NewLoader(boot *BootConfig) Loader
```

### 3.2 启动降级链（多容器专用，任何一步失败只是降级，绝不退出）

```
① 镜像内嵌 config/config.yaml 加载         ← 保底：build 时打入，必然存在
   │ 失败 → 启动失败（唯一失败点，可接受：镜像缺陷）
② 尝试连 Redis，读 gw:config:snapshot      ← 次新：发布时写入，多实例一致
   │ 命中 → 用快照替换 Endpoints；miss/挂 → 继续
③ 尝试连 MySQL，SELECT 启用路由            ← 最新：事实源（gateway 自有库）
   │ 成功 → 用 DB 结果替换 + 回写 Redis 快照；挂 → 继续
④ 启动完成，开始服务（用当前拥有的最旧配置）→ 同时启动 ⑤⑥
```

多容器下**本地文件快照不可用**（Pod 文件系统不共享），故用 Redis 共享快照替代；Redis 也挂时以镜像内嵌配置 + 内存旧配置继续服务。

### 3.3 运行时更新

```go
// Watch 实现（伪代码）
func (l *loader) Watch(ctx context.Context, fn func(*Config)) {
    // ⑤ PubSub 订阅：通知只是触发器，内容以回源拉取为准（容忍丢消息）
    go l.subscribe(ctx, func(version int64) {           // 收到 gw:config:changed
        cfg, err := l.fetchFromDB()                     // 回源全量拉取
        if err != nil { cfg, err = l.fetchFromRedis() } // DB 挂 → 读快照
        if err != nil { return }                        // 都挂 → 保持现状，等对账
        fn(cfg)                                         // → proxy.Update() 原子替换
    })
    // ⑥ 周期对账：防丢通知、防组件短暂故障，60s 一次
    go l.reconcile(ctx, fn)
}
```

**热更复用现有机制**：`proxy.Update()`（`proxy/proxy.go:444-461`）已经是"构建新路由 → `router.Swap` 原子替换 → 旧路由 120s 优雅关闭"，**转发核心零改动**。

### 3.4 数据库接入（新增）

- 引入 `gorm.io/gorm` + `gorm.io/driver/mysql`（与生态一致的 GORM 封装；如有必要可下沉 kratosx `library/db`，配置结构不与生态冲突）；
- `config/config.yaml` 的 `database:` 段按 kratosx 标准结构声明（`enable/drive/connect`），Loader 初始化时 `gorm.Open` 建连，`SetMaxOpenConns` 等按 kratosx 惯例；
- 路由读取仅存在于 Loader（`fetchFromDB`），**转发路径（`RoundTrip`）零 DB 访问**，吞吐不受 DB 影响。

### 3.5 管理 API（gateway 自持，`/gateway/api/*`）

管理 API 由 gateway 自身实现（新增 `admin/` 包），与传统请求的区别：**不经过反向代理路由表**（鸡生蛋问题），在主 handler 上按 `/gateway/api/` 前缀拦截（复用 debug 包 `MashupWithDebugHandler` 的前缀匹配模式，但独立于 debug 开关）：

```
GET    /gateway/api/endpoints              # 路由列表（分页 + 过滤）
POST   /gateway/api/endpoints              # 新建（草稿态，不生效）
PUT    /gateway/api/endpoints              # 修改（草稿态，id 经 body 传递）
DELETE /gateway/api/endpoints              # 删除（草稿态，id 经 query 传递）
POST   /gateway/api/publish                # 发布：全量校验 → 事务写库 → 快照 → 通知（见 4.2）
POST   /gateway/api/publish/rollback       # 回滚：按 history 版本恢复并重新发布
GET    /gateway/api/publish/history        # 发布历史 + 全量快照查看
```

- **路径风格**：按项目约定**不使用 `{id}` 路径参数**（如 `/manager/api/app`），按 id 操作经 body/query 传递；
- **鉴权**：复用现有 `auth` 中间件（调 manager 的 auth 服务校验 token，`config/config.yaml` 已有此模式），白名单仅 `OPTIONS`；
- **前端**：页面挂在 admin-web（与 resource/configure 同款 vue 页面），路由/组件注册通过菜单完成。

### 3.6 菜单自注册（启动时一次）

参考 `resource/internal/initiator/menu/`、`configure/internal/initiator/menu/` 的既有模式，gateway 新增 `internal/initiator/menu/`：

```go
// 启动 AfterStart 回调
func registerMenu() {
    conn, _ := grpc.DialInsecure(ctx, grpc.WithEndpoint(conf.ManagerAddr)) // manager 地址，config.yaml 配置
    _, _ = managerMenu.NewMenuClient(conn).RegisterMenu(ctx, &managerMenu.RegisterMenuRequest{
        App: "gateway", // 标识提供方
        Menus: []*managerMenu.RegisterMenuNode{
            group("网关配置", "gateway", "icon", false,
                page("路由管理", "gateway-endpoints", "/gateway/endpoints", "views/gateway/endpoints/index",
                    apiPerm("路由列表",   "GET",    "/gateway/api/endpoints", "gateway:endpoint:list"),
                    apiPerm("发布配置",   "POST",   "/gateway/api/publish",   "gateway:publish"),
                    // ... 每个管理 API 一个权限点
                ),
            ),
        },
    })
}
```

效果：登录 manager 后菜单出现「网关配置 → 路由管理」，角色可分配 `gateway:*` 权限点；**manager 侧零代码改动**（`RegisterMenu` 已存在且被 resource/configure 使用）。

### 3.7 移除 configure 依赖

- `main.go`：删除 `configSource()` 与 `configure.New` 分支，改 `NewLoader`；
- 删除 `go.mod` 中 `github.com/limes-cloud/configure v1.0.39`；
- 删除 `.env` 中 `CONF_HOST` / `CONF_TOKEN` / `APP_NAME`（已从 git 移除，见历史）。

### 3.8 新的 `config/config.yaml`（启动引导）

```yaml
# 实例级配置：只描述"如何启动"（网络/连接），不再承载路由
debug: false
addr: 0.0.0.0:7080
name: gateway
version: v1
managerAddr: "127.0.0.1:9000"   # manager gRPC 地址（菜单注册/鉴权用）

database:                        # gateway 自有库（kratosx 标准结构）
  - enable: true
    name: default
    drive: mysql
    connect: { host: "127.0.0.1", port: 3306, dbName: gateway, username: "", password: "" }
redis:
  - enable: true
    name: cache
    host: "127.0.0.1:6379"
```

> `db.enable=false` 或未配置时网关退化为"仅本地文件纯静态模式"，本地开发/单机部署仍可用。

---

## 四、发布事务（唯一生效入口，改表不自动生效）

```
① 全量校验（schema）：path 格式、protocol 枚举、backends 非空、retry 条件合法、中间件名已注册
   └─ 失败 → 返回错误列表，不产生任何变更
② 事务：UPDATE/DELETE/INSERT gw_endpoint（草稿变更落库）
③ 生成版本号（DB sequence / redis INCR，取全局自增）
④ 写 gw_endpoint_history（该版本全量下发配置快照 + operator + remark）
⑤ 全量序列化启用路由（含全局中间件）→ SET gw:config:snapshot <json>（含 version 字段）
⑥ PUBLISH gw:config:changed {version, ts}
⑦ 返回版本号；网关侧 60s 内全部生效（PubSub 秒级 + 对账兜底）
```

**约束**：
- 网关实例**永不主动读 `gw_endpoint` 做"改表即生效"**——一切以发布事务产出的快照/通知为准，保证各实例版本一致；
- `enabled=0` 的路由不下发、不参与快照，仅保留在表内；
- 发布是**全量**快照语义（版本可回滚），不是增量 diff；
- 发布 API 所在进程即普通网关实例（任一实例可执行），多实例并发发布由版本号唯一键（`uk_gw_endpoint_history_version`）兜底，冲突时报错重试。

---

## 五、迁移方案（从 configure 到 MySQL）

### 5.1 迁移前状态

- 线上 6 条路由在 configure 的 app 配置中（`config/config.yaml` 同构）；
- 网关部署为多容器，配置中心与网关同集群。

### 5.2 迁移步骤（全量替换，无长期双写）

| 步 | 动作 | 校验 |
|---|---|---|
| 1 | 新镜像：Loader 重构 + `config.yaml` 引导结构（含 MySQL/Redis/manager 地址），**旧 `endpoints` 仍保留在 yaml 中作为保底** | 单实例本地起服务，`/debug/proxy/router/inspect` 确认路由加载 |
| 2 | 建库建表：执行第二节 DDL（gateway 库） | `SHOW TABLES` 存在 |
| 3 | 现有 6 条路由导入 `gw_endpoint`（seed 脚本，从现有 `config.yaml` 逐条映射） | `SELECT COUNT(*) = 6` |
| 4 | 灰度：先滚动升级 **1 个** 网关实例，验证（路由加载正常 → 跑通 manager/resource 两条链路 smoke → 管理 API 可发布） | 发布/回滚 API 可用 |
| 5 | 滚动升级**全部**实例（新镜像启动即从 MySQL 拉取；configure 已不再被引用） | 各实例 `/debug/watcher` 与路由 inspect 一致 |
| 6 | 下线：删除 configure 中 gateway 的 app 配置；清理 `CONF_*` 环境变量 | 无实例报错 |

### 5.3 回滚计划

| 部署步骤 | 回滚动作 | 回滚时长 |
|---|---|---|
| 步骤 1-2（镜像/SQL） | 回退镜像到旧版本，`DROP TABLE` 回退 SQL | 分钟级 |
| 步骤 3（seed） | seed 幂等（`ON DUPLICATE KEY`），直接重跑 | 分钟级 |
| 步骤 4-5（灰度/全量） | 回滚镜像版本 → 旧镜像仍读 configure 或本地文件，**发布前备份 configure 中 app 配置** | 分钟级 |
| 步骤 6（下线） | configure 配置删除不可逆——**本步为单向前进点**，仅在 1-5 全部验证通过后执行 | - |

> 无"不可逆"节点：步骤 1-5 的任意时刻回滚 = 回退镜像 + 恢复 configure 备份（发布前先导出备份一份）；步骤 6 才真正移除 configure，其前必须完成全量验证。

---

## 六、Lens 评估

| Lens | 结论 |
|---|---|
| L0 必要性 | 是——启动强依赖 + 无管理/审计 + 无版本回滚，四个问题都指向存储重构 |
| L1 备选 | configure 改进（被否决：模型是 app 级大串，管理面无从下手）；etcd（引入新组件，与管理面集成成本更高）；MySQL（已有组件，Kong 同款模式）→ 选 MySQL |
| L2 失败语义 | 组件挂：DB 挂 → 读 Redis 快照；Redis 挂 → 内存 + 文件；全部挂 → 仅配置不更新，**服务不中断**。10x 流量：路由表行数（百级）与请求路径完全解耦，无放大；快照序列化在发布时一次，与 QPS 无关 |
| L3 可逆性 | 高——网关零转发逻辑改动；remove configure 前置备份即可完整回退；表结构幂等（`IF NOT EXISTS`），可重复执行 |

---

## 七、未决问题

| # | 问题 | 建议默认 | 需确认 |
|---|---|---|---|
| 1 | 管理 API 挂载点 | **已确认：`/gateway/api/*` 前缀**（主 handler 前缀拦截，复用 debug 的 Mashup 模式，不新增端口） | - |
| 2 | 版本号生成：DB `AUTO_INCREMENT` 还是 Redis `INCR` | DB（与发布事务同库，天然一致；history 表用独立自增即可） | - |
| 3 | 发布是否需要审批流（如双人确认） | 暂不做（审计记录已满足），后续可基于 history 扩展 | 是否有合规要求 |
| 4 | 灰度粒度：先"全量实例同版本"，是否需要按实例灰度 | 不做，仅支持版本回滚 | - |
| 5 | 对账周期 60s 是否可接受（发布到全量生效的理论上限） | 接受（PubSub 秒级已覆盖 99% 场景，对账仅兜底） | - |
| 6 | 管理 API 鉴权：复用 `auth` 中间件（manager auth 服务）即可，还是需要更细的 `app=gateway` 权限隔离 | 复用 auth 中间件 + 菜单权限点（`gateway:*`，由 manager 鉴权） | - |
| 7 | `metadata` 等 JSON 列允许 NULL（MySQL 限制），由应用层兜底空对象 | 接受 | - |
