// 任务模式：
// summary = 项目总结
// code    = 改代码输出 diff
// chat    = 问答/建议，不输出 diff
export type AgentMode = 'summary' | 'code' | 'chat';

// 最小 chunk 结构：由 indexer 产出
export interface ChunkRecord {
    id: number;        // chunk 唯一 id（在一次索引中递增）
    relPath: string;   // 相对项目根目录路径
    text: string;      // chunk 文本内容
}

// 检索命中结构：在 ChunkRecord 基础上附带各类分数
export interface RetrievedHit extends ChunkRecord {
    score: number;         // 融合后的最终分数（用于排序）
    bm25Score: number;     // 原始 BM25 最高分
    queryCoverage: number; // query 覆盖率（命中 query 数 / 总 query 数）
    pathBoost: number;     // 路径匹配加分
    llmScore?: number;     // LLM rerank 分数（可选）
}
