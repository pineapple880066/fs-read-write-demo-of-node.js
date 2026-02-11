// chunk 大小：越大保留上下文越多，但每块更“粗”
export const CHUNK_SIZE = Number(process.env.CHUNK_SIZE || 800);

// chunk 重叠长度：避免重要语义正好被切断
export const CHUNK_OVERLAP = Number(process.env.CHUNK_OVERLAP || 120);

// 最终喂给模型的命中数量（最终 topK）
export const RAG_TOP_K = Number(process.env.RAG_TOP_K || 8);

// 第一阶段 BM25 召回数量（一般比 RAG_TOP_K 大）
export const RAG_RECALL_K = Number(process.env.RAG_RECALL_K || 40);

// 进入 LLM 重排的候选数量
export const RAG_RERANK_CANDIDATES = Number(process.env.RAG_RERANK_CANDIDATES || 20);

// 是否启用 LLM rerank（0 = 关闭，其余 = 开启）
export const RAG_ENABLE_RERANK = String(process.env.RAG_ENABLE_RERANK || '1') !== '0';

// 构建上下文时的字符预算上限
export const RAG_READ_CHARS = Number(process.env.RAG_READ_CHARS || 4000);

// 停用词：在 tokenize 时过滤，减少噪声词影响
export const STOP_WORDS: ReadonlySet<string> = new Set([
    '的', '了', '和', '是', '在', '我', '要', '把',
    'to', 'the', 'a', 'an', 'for', 'and', 'or', 'is', 'are',
]);
