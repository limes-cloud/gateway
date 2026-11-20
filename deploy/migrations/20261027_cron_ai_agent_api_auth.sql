-- Protect cron and ai-agent gateway routes with manager token validation.
SET NAMES utf8mb4;

UPDATE `gw_endpoint`
SET `middlewares` = '[{"name":"auth","options":{"url":"http://127.0.0.1:7202/manager/api/v1/authorize/token/parse","method":"POST","contentType":"application/json;charset=utf8","whitelist":[{"path":"/*","method":"OPTIONS"}]}}]'
WHERE `path` IN ('/cron/api/*', '/ai-agent/api/*');
