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
- Browser UI:
  - `GET /`
  - Codex-like local codebase Q&A shell
  - auto bootstrap local JWT for browser session
- Unified response body:
  - success: `{ data, request_id }`
  - error: `{ code, message, request_id }`
- Middleware:
  - request id
  - JWT auth
  - Redis-backed fixed-window rate limit
- Chat memory:
  - Redis session short-term memory (recent N messages + TTL)
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

4. open UI:

```bash
open http://127.0.0.1:8080/
```

If you want real LLM responses (not fallback text), make sure `LLM_API_KEY` is set in your runtime env.
When using Docker Compose, export it before startup:

```bash
export LLM_API_KEY=your_api_key
docker compose -f deploy/docker-compose.yml up -d --force-recreate app
```

## API contracts (fixed)

- `POST /v1/chat`
  - req: `tenant_id/session_id/user_id/message/mode/root_dir(optional)`
  - resp: `answer/evidence_files/retrieval_debug/task_id(optional)`
- `POST /v1/ingest`
  - req: `tenant_id/source_type/source_uri/text(optional)`
  - resp: `task_id/status`
- `POST /v1/search`
  - req: `tenant_id(optional when root_dir is provided)/query/top_k/root_dir(optional)`
  - resp: `hits[{chunk_id,rel_path,score,bm25_score,dense_score}]`
- `GET /v1/tasks/:id`
  - resp: `task_id/status/progress/error_message/result_ref`

## Retrieval fusion formula

`final = 0.45*bm25_norm + 0.30*dense_norm + 0.15*query_coverage + 0.10*path_boost`

## Notes

- Current search pipeline includes scaffolding and fixed-form score generation.
- Week-6 target is to replace placeholders with true BM25 + dense(Milvus) + rerank fusion.

### Local codebase scan (Codex-like API usage)

You can pass `root_dir` in `/v1/search` or `/v1/chat` to let the backend scan a local directory and retrieve code context.

Example:

```bash
curl -s -X POST http://127.0.0.1:8080/v1/search \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{
    "tenant_id":"t1",
    "query":"explain task consumer flow",
    "top_k":6,
    "root_dir":"/Users/pineapple/Desktop/javascript/agent_server_go"
  }'
```

If app runs in Docker, use container-visible paths (e.g. `/workspace/...`), not host-only paths.

The app container includes both Go and Node runtime (`deploy/Dockerfile.app`) so TS bridge scanning can run inside Docker.

### Chat context memory

`/v1/chat` now loads recent session messages from Redis before model call, and appends current `user/assistant` messages after reply.

Before sending to LLM, service will auto-fit history by model context window:
- tries to detect model max context tokens from `/models` (or uses fallback guess)
- computes history token budget
- keeps recent turns first
- summarizes old turns with LLM when over budget, then feeds summary + recent turns

Env knobs:
- `CHAT_MEMORY_MAX_MESSAGES` (default: `12`)
- `CHAT_MEMORY_TTL_SECONDS` (default: `1800`)
- `LLM_HTTP_TIMEOUT_SECONDS` (default: `60`)
- `LLM_REQUEST_BUDGET_SECONDS` (default: `180`)
- `LLM_MAX_CONTEXT_TOKENS` (default: `0`, means auto-detect/guess)

### Built-in browser UI

Root path `/` now serves a Codex-like single-page UI.

Current scope:
- create local threads/tasks
- set `root_dir`
- ask codebase questions through `/v1/chat`
- edit files through `/v1/chat` in `edit` mode
- show evidence files and retrieval debug

Current limits:
- browser cannot open native folder picker with a real absolute path, so `root_dir` is currently typed manually
- local AST parsing currently targets `.go` files; other languages still use text chunk retrieval
