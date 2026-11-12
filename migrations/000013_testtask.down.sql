-- migrations/000013_testtask.down.sql
-- 回滚测试执行域。逆序 DROP：无上层表依赖这三张表。

DROP INDEX IF EXISTS idx_results_trace;
DROP INDEX IF EXISTS idx_results_case;
DROP INDEX IF EXISTS idx_results_actor;
DROP INDEX IF EXISTS idx_results_task_result;
DROP INDEX IF EXISTS uniq_result_task_case_run;
DROP TABLE IF EXISTS test_results;

DROP INDEX IF EXISTS uniq_ttc_task_case;
DROP INDEX IF EXISTS idx_ttc_task;
DROP TABLE IF EXISTS test_task_cases;

DROP INDEX IF EXISTS idx_test_tasks_name;
DROP INDEX IF EXISTS idx_test_tasks_product_status;
DROP TABLE IF EXISTS test_tasks;

INSERT INTO schema_meta (singleton, version)
VALUES (true, 12)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
