-- resource API 接入网关鉴权；仅保留跨域预检和两个公开图片读取接口。
SET NAMES utf8mb4;

UPDATE `gw_endpoint`
SET `middlewares` = '[{"name":"auth","options":{"url":"http://127.0.0.1:7202/manager/api/v1/authorize/token/parse","method":"POST","contentType":"application/json;charset=utf8","whitelist":[{"path":"/resource/api/file/object-by-key","method":"GET"},{"path":"/resource/api/file/read-by-key","method":"GET"},{"path":"/*","method":"OPTIONS"}]}}]'
WHERE `path` = '/resource/api/*' AND `method` = '';
