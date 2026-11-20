-- psou 客户端 BFF 路由：前端通过网关访问 /psou/client/*。
SET NAMES utf8mb4;

INSERT INTO `gw_endpoint` (`path`, `title`, `method`, `host`, `protocol`, `response_format`, `timeout_ms`, `description`, `metadata`, `backends`, `middlewares`, `retry`, `enabled`)
VALUES
  ('/psou/client/*', '盘搜客户端 API', '', '', 'HTTP', 1, 60000, 'psou 客户端 BFF', NULL, '[{"target":"127.0.0.1:7030"}]', NULL, NULL, 1)
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
