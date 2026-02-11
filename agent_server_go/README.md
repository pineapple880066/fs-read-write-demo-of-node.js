# Agent Server (Go Fiber)

This is the Go service track for your dual-stack plan:
- Baseline CLI: `../agent` (TypeScript)
- Service mainline: `./agent_server_go` (Go + Fiber)

## What is implemented now

- API endpoints:
  - `POST /v1/chat`
  - `POST /v1/ingest`
  - `POST /v1/search`
  - `GET /v1/tasks/:id`
- Unified response body:
  - success: `{ data, request_id }`
  - error: `{ code, message, request_id }`
- Middleware:
  - request id
  - JWT auth
  - Redis-backed fixed-window rate limit
- Data and infra modules:
  - MySQL store (`internal/store/mysql`)
  - Redis cache (`internal/cache/redis`)
  - RabbitMQ producer/consumer (`internal/mq/rabbitmq`)
  - model gateway (OpenAI-compatible)
  - retrieval scoring and query sanitization
- Migrations:
  - `migrations/001_init.sql`
  - `migrations/001_init_down.sql`
- Deploy stack:
  - app + mysql + redis + rabbitmq + milvus + prometheus + grafana + jaeger

## Local run

1. copy env:

```bash
cp .env.example .env
```

2. run dependencies:

```bash
make compose-up
```

3. run server:

```bash
make run
```

## API contracts (fixed)

- `POST /v1/chat`
  - req: `tenant_id/session_id/user_id/message/mode`
  - resp: `answer/evidence_files/retrieval_debug/task_id(optional)`
- `POST /v1/ingest`
  - req: `tenant_id/source_type/source_uri/text(optional)`
  - resp: `task_id/status`
- `POST /v1/search`
  - req: `tenant_id/query/top_k`
  - resp: `hits[{chunk_id,rel_path,score,bm25_score,dense_score}]`
- `GET /v1/tasks/:id`
  - resp: `task_id/status/progress/error_message/result_ref`

## Retrieval fusion formula

`final = 0.45*bm25_norm + 0.30*dense_norm + 0.15*query_coverage + 0.10*path_boost`

## Notes

- Current search pipeline includes scaffolding and fixed-form score generation.
- Week-6 target is to replace placeholders with true BM25 + dense(Milvus) + rerank fusion.
