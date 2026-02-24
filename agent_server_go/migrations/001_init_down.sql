-- 回滚顺序采用“先子表/日志表，后主表”的方式，避免依赖关系报错。
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS retrieval_logs;
DROP TABLE IF EXISTS chunks;
DROP TABLE IF EXISTS documents;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
