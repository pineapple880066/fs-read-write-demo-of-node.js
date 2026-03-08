# 8-Week Sprint Roadmap (Go Fiber Track)

## Week 1
- [x] Go service skeleton initialized
- [x] Fiber app, health endpoint, basic logger
- [x] Gemini CLI study template prepared

## Week 2
- [x] API contracts landed: `/chat` `/ingest` `/search` `/tasks`
- [x] request_id + error body + success body
- [x] service layer skeleton for chat/ingest/search/tasks

## Week 3
- [x] MySQL migrations and repository scaffold
- [ ] Complete session/message/document CRUD paths

## Week 4
- [x] Redis cache/rate-limit scaffold
- [ ] Cache invalidation on document/chunk updates

## Week 5
- [x] RabbitMQ producer/consumer scaffold
- [ ] Retry, DLQ, task state transitions with observability

## Week 6
- [x] Retrieval package with fixed fusion formula
- [ ] Real BM25 + dense(Milvus) + rerank implementation

## Week 7
- [x] Prometheus metrics endpoint
- [x] OTel tracer provider scaffold
- [ ] Full tracing spans and model-gateway fallback metrics

## Week 8
- [x] CI workflow scaffold
- [ ] JWT login issuance endpoint
- [ ] Tenant-level isolation checks
- [ ] Benchmark + offline eval report