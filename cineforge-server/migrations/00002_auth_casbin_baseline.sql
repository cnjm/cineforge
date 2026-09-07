-- ============================================================
-- 00002 · auth 授权基线种子数据
-- 内容：5 个平台角色（sys_role）+ casbin 能力策略（sys_casbin_rule）
-- 权限矩阵基线（5 角色基础授权）
--   5 角色 × {projects.read, tasks.read, flows.write, tasks.submit} × execute
--   tasks.review 仅 admin/director（article 特权）
-- 单库 monolith：sys_role / sys_casbin_rule 是授权权威数据（users 为唯一身份），
-- 首启时由本 migration 一次性写入；改动走后续 goose migration，不允许运行时 DDL。
-- ============================================================

-- +goose Up

INSERT INTO sys_role (role_name, status, role_key, role_sort, remark)
SELECT '平台管理员', '2', 'admin', 1, 'cineforge platform role'
WHERE NOT EXISTS (SELECT 1 FROM sys_role WHERE role_key = 'admin');

INSERT INTO sys_role (role_name, status, role_key, role_sort, remark)
SELECT '负责人', '2', 'director', 2, 'cineforge platform role'
WHERE NOT EXISTS (SELECT 1 FROM sys_role WHERE role_key = 'director');

INSERT INTO sys_role (role_name, status, role_key, role_sort, remark)
SELECT '脚本编辑', '2', 'script_editor', 3, 'cineforge platform role'
WHERE NOT EXISTS (SELECT 1 FROM sys_role WHERE role_key = 'script_editor');

INSERT INTO sys_role (role_name, status, role_key, role_sort, remark)
SELECT '美术', '2', 'artist', 4, 'cineforge platform role'
WHERE NOT EXISTS (SELECT 1 FROM sys_role WHERE role_key = 'artist');

INSERT INTO sys_role (role_name, status, role_key, role_sort, remark)
SELECT '剪辑', '2', 'editor', 5, 'cineforge platform role'
WHERE NOT EXISTS (SELECT 1 FROM sys_role WHERE role_key = 'editor');

-- 5 角色通用能力
INSERT INTO sys_casbin_rule (ptype, v0, v1, v2)
SELECT 'p', r.role_key, c.capability, c.action
FROM sys_role r
CROSS JOIN (VALUES
    ('projects.read', 'execute'),
    ('tasks.read', 'execute'),
    ('flows.write', 'execute'),
    ('tasks.submit', 'execute')
) AS c(capability, action)
WHERE NOT EXISTS (
    SELECT 1 FROM sys_casbin_rule e
    WHERE e.ptype = 'p' AND e.v0 = r.role_key AND e.v1 = c.capability AND e.v2 = c.action
);

-- tasks.review 仅 admin/director
INSERT INTO sys_casbin_rule (ptype, v0, v1, v2)
SELECT 'p', role_key, 'tasks.review', 'execute'
FROM sys_role
WHERE role_key IN ('admin', 'director')
  AND NOT EXISTS (
    SELECT 1 FROM sys_casbin_rule e
    WHERE e.ptype = 'p' AND e.v0 = sys_role.role_key AND e.v1 = 'tasks.review'
  );

-- +goose Down

DELETE FROM sys_casbin_rule WHERE v1 IN ('projects.read', 'tasks.read', 'flows.write', 'tasks.submit', 'tasks.review');
DELETE FROM sys_role WHERE role_key IN ('admin', 'director', 'script_editor', 'artist', 'editor');