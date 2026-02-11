import type { ChunkRecord } from './types.js';

const EPS = 1e-6;

// 加入 tokens 后的文档结构（用于 BM25 计算）
export interface IndexedDoc extends ChunkRecord {
    tokens: string[];
}

// BM25 索引结构
export interface Bm25Index {
    docs: IndexedDoc[];       // 所有文档（这里文档=chunk）
    df: Map<string, number>;  // document frequency：词在多少文档中出现过
    avgLen: number;           // 平均文档长度
    N: number;                // 文档总数
}

// 单条检索结果
export interface Bm25Score {
    doc: IndexedDoc;
    score: number;
}

// 简单 tokenizer：英数下划线+中文，去停用词、去长度1词
function tokenize(text: string, stopWords: ReadonlySet<string>): string[] {
    return String(text)
        .toLowerCase()
        .split(/[^a-z0-9_\u4e00-\u9fa5]+/)
        .filter(t => t && !stopWords.has(t) && t.length > 1);
}

// 构建 BM25 所需索引
export function buildBm25Index(chunks: ChunkRecord[], stopWords: ReadonlySet<string>): Bm25Index {
    const docs: IndexedDoc[] = chunks.map(c => ({
        id: c.id,
        relPath: c.relPath,
        text: c.text,
        tokens: tokenize(c.text, stopWords),
    }));

    const df = new Map<string, number>();
    let totalLen = 0;

    for (const d of docs) {
        totalLen += d.tokens.length;

        // 同一文档中同一词只给 df 记一次
        const uniq = new Set(d.tokens);
        for (const t of uniq) {
            df.set(t, (df.get(t) || 0) + 1);
        }
    }

    return {
        docs,
        df,
        avgLen: docs.length ? totalLen / docs.length : 0,
        N: docs.length,
    };
}

// BM25 检索并返回 topK
export function bm25Search(
    index: Bm25Index,
    query: string,
    stopWords: ReadonlySet<string>,
    topK = 8,
): Bm25Score[] {
    // 标准 BM25 参数：
    // k1 控制词频饱和速度，b 控制长度惩罚强度
    const k1 = 1.2;
    const b = 0.75;

    const qTokens = tokenize(query, stopWords);

    const scores: Bm25Score[] = index.docs.map(d => {
        // tf: 词在当前文档出现的次数
        const tf = new Map<string, number>();
        for (const t of d.tokens) {
            tf.set(t, (tf.get(t) || 0) + 1);
        }

        let score = 0;
        for (const t of qTokens) {
            const f = tf.get(t) || 0;
            if (!f) continue;

            const df = index.df.get(t) || 0;
            // idf：词越稀有，权重越大
            const idf = Math.log(1 + (index.N - df + 0.5) / (df + 0.5));
            // 文档长度归一化分母
            const denom = f + k1 * (1 - b + b * (d.tokens.length / (index.avgLen || 1)));

            score += idf * ((f * (k1 + 1)) / (denom + EPS));
        }

        return { doc: d, score };
    });

    return scores.sort((a, b) => b.score - a.score).slice(0, topK);
}
