-- 管理端网关入口收敛为 /manager/*；manager 自身仍使用 /manager/api/* 路径。
SET NAMES utf8mb4;

UPDATE `gw_endpoint`
SET `path` = '/manager/*',
    `description` = 'manager 管理端 API（保留后端 /manager/api/* 路径）'
WHERE `path` = '/manager/api/*';
