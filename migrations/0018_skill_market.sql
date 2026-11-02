-- +goose Up
CREATE TABLE skill_catalog (
 id uuid PRIMARY KEY, tenant_id uuid REFERENCES tenants(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 64),
 description text NOT NULL CHECK(length(description) BETWEEN 1 AND 1024),
 body text NOT NULL CHECK(octet_length(body) BETWEEN 1 AND 32768),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE NULLS NOT DISTINCT (tenant_id,name)
);
CREATE TABLE skill_favorites (
 tenant_id uuid NOT NULL REFERENCES tenants(id), skill_id uuid NOT NULL REFERENCES skill_catalog(id) ON DELETE CASCADE,
 PRIMARY KEY(tenant_id,skill_id)
);
CREATE TABLE sandbox_skills (
 sandbox_id uuid PRIMARY KEY REFERENCES sandboxes(id) ON DELETE CASCADE,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 skill_ids uuid[] NOT NULL DEFAULT '{}',
 version bigint NOT NULL DEFAULT 1,
 applied_version bigint NOT NULL DEFAULT 0,
 sync_status text NOT NULL DEFAULT 'pending' CHECK(sync_status IN ('pending','applied','failed'))
);
CREATE TRIGGER validate_skill_owner BEFORE INSERT OR UPDATE ON sandbox_skills
 FOR EACH ROW EXECUTE FUNCTION validate_mcp_binding_owner();
-- +goose StatementBegin
CREATE FUNCTION cleanup_sandbox_skills() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.desired_state='deleted' OR NEW.deleted_at IS NOT NULL OR NEW.deletion_requested_at IS NOT NULL THEN
  DELETE FROM sandbox_skills WHERE sandbox_id=NEW.id;
 END IF;
 RETURN NEW;
END; $$;
-- +goose StatementEnd
CREATE TRIGGER cleanup_sandbox_skills AFTER UPDATE ON sandboxes
 FOR EACH ROW EXECUTE FUNCTION cleanup_sandbox_skills();
INSERT INTO skill_catalog(id,name,description,body) VALUES
 ('11000000-0000-4000-8000-000000000001','code-review','审查代码正确性、安全边界与验证证据。','先明确改动目标，再检查鉴权、输入校验、错误处理、并发及资源清理。按严重程度报告可以复现的问题，提供文件位置与修复建议。区分已运行的测试与尚未验证的推断；没有发现问题时说明验证范围。'),
 ('11000000-0000-4000-8000-000000000002','debugging','根据证据定位问题并验证修复。','先记录现象和最小复现，再逐步缩小故障范围。每次只验证一个假设，优先读取错误与调用链。修改前说明根因和最小修复，修复后执行针对性回归，不把尝试过当作验证成功。'),
 ('11000000-0000-4000-8000-000000000003','write-tests','为实际行为编写有意义的测试。','明确输入、预期行为与失败条件。测试应覆盖关键正常流程、边界与错误处理，避免复制实现或只检查内部细节。沿用项目已有测试框架，执行相关测试并报告实际结果。');
-- +goose Down
DROP TRIGGER cleanup_sandbox_skills ON sandboxes;
DROP FUNCTION cleanup_sandbox_skills();
DROP TABLE sandbox_skills;
DROP TABLE skill_favorites;
DROP TABLE skill_catalog;
