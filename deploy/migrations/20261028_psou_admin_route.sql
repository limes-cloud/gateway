-- psou 管理端路由：前端通过 gateway 访问 /psou/admin/*，由 manager 校验登录态。
SET NAMES utf8mb4;

INSERT INTO `gw_endpoint` (`path`, `title`, `method`, `host`, `protocol`, `response_format`, `timeout_ms`, `description`, `metadata`, `backends`, `middlewares`, `retry`, `enabled`)
VALUES
  ('/psou/admin/*', '盘搜管理 API', '', '', 'HTTP', 1, 60000, 'psou 网盘账号与在线管理 API', NULL, '[{"target":"127.0.0.1:7301"}]', '[{"name":"auth","options":{"url":"http://127.0.0.1:7202/manager/api/v1/authorize/token/parse","method":"POST","contentType":"application/json;charset=utf8","whitelist":[{"path":"/*","method":"OPTIONS"}]}}]', NULL, 1)
ON DUPLICATE KEY UPDATE
  `title`           = VALUES(`title`),
  `protocol`        = VALUES(`protocol`),
  `response_format` = VALUES(`response_format`),
  `timeout_ms`      = VALUES(`timeout_ms`),
  `description`     = VALUES(`description`),
  `backends`        = VALUES(`backends`),
  `middlewares`     = VALUES(`middlewares`),
  `retry`           = VALUES(`retry`),
  `enabled`         = VALUES(`enabled`);
