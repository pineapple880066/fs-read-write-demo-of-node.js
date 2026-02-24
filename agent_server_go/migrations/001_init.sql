-- 初始化核心业务表：
-- users / sessions / messages / documents / chunks / retrieval_logs / tasks
-- 当前是第一版固定 schema，后续变更建议新增 migration，而不是直接修改本文件。

CREATE TABLE IF NOT EXISTS users (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  tenant_id VARCHAR(64) NOT NULL,
  email VARCHAR(255) NOT NULL,
  password_hash VARCHAR(255) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_users_tenant_email (tenant_id, email)
);

CREATE TABLE IF NOT EXISTS sessions (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  tenant_id VARCHAR(64) NOT NULL,
  user_id BIGINT NOT NULL,
  title VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  KEY idx_sessions_tenant_user_updated (tenant_id, user_id, updated_at)
);

CREATE TABLE IF NOT EXISTS messages (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  tenant_id VARCHAR(64) NOT NULL,
  session_id BIGINT NOT NULL,
  role VARCHAR(32) NOT NULL,
  content TEXT NOT NULL,
  token_in INT NOT NULL DEFAULT 0,
  token_out INT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_messages_tenant_session_created (tenant_id, session_id, created_at)
);

CREATE TABLE IF NOT EXISTS documents (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  tenant_id VARCHAR(64) NOT NULL,
  source_type VARCHAR(64) NOT NULL,
  source_uri VARCHAR(1024) NOT NULL,
  checksum VARCHAR(128) NOT NULL,
  status VARCHAR(32) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_documents_tenant_checksum (tenant_id, checksum)
);

CREATE TABLE IF NOT EXISTS chunks (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  tenant_id VARCHAR(64) NOT NULL,
  document_id BIGINT NOT NULL,
  rel_path VARCHAR(1024) NOT NULL,
  chunk_index INT NOT NULL,
  text MEDIUMTEXT NOT NULL,
  token_count INT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_chunks_tenant_doc_idx (tenant_id, document_id, chunk_index)
);

CREATE TABLE IF NOT EXISTS retrieval_logs (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  tenant_id VARCHAR(64) NOT NULL,
  session_id VARCHAR(64) NOT NULL,
  query TEXT NOT NULL,
  top_k INT NOT NULL,
  hit_ids_json JSON NOT NULL,
  latency_ms BIGINT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_retrieval_logs_tenant_session_created (tenant_id, session_id, created_at)
);

CREATE TABLE IF NOT EXISTS tasks (
  id VARCHAR(64) PRIMARY KEY,
  tenant_id VARCHAR(64) NOT NULL,
  type VARCHAR(64) NOT NULL,
  status VARCHAR(32) NOT NULL,
  payload_json JSON NOT NULL,
  error_message TEXT NULL,
  retry_count INT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  KEY idx_tasks_tenant_status_updated (tenant_id, status, updated_at)
);
