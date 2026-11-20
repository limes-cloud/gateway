-- psou 公共 API 路由：H5、小程序和 PC 通过 gateway 访问 /psou/api/*。
SET NAMES utf8mb4;

INSERT INTO `gw_endpoint` (`path`, `title`, `method`, `host`, `protocol`, `response_format`, `timeout_ms`, `description`, `metadata`, `backends`, `middlewares`, `retry`, `enabled`)
VALUES
  ('/psou/api/*', '盘搜公共 API', '', '', 'HTTP', 1, 60000, 'psou H5、小程序与 PC 公共 API', NULL, '[{"target":"127.0.0.1:7301"}]', NULL, NULL, 1)
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
