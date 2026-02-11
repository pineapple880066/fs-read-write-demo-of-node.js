import { RAG_TOP_K, RAG_READ_CHARS, RAG_RECALL_K, STOP_WORDS } from './config.js';
import { indexProject } from './indexer.js';
import { buildBm25Index, bm25Search } from './bm25.js';
import type { RetrievedHit } from './types.js';

interface RetrieveInput {
    rootDir: string;
    files: string[];
    query: string;
    queryVariants?: string[];
    topK?: number;
    recallK?: number;
    stopWords?: ReadonlySet<string>;
}

interface BuildRagDataInput {
    rootDir: string;
    files: string[];
    query: string;
    queryVariants?: string[];
    topK?: number;
}

// query 提示结构：
// fullPaths = 像 a.js / src/app.ts 这种完整路径关键词
// segments  = 像 agent / retrieve 这种普通片段关键词
interface PathHints {
    fullPaths: Set<string>;
    segments: Set<string>;
}

// 多 query 融合时，对同一个 chunk 的中间统计
interface MergedStats {
    doc: { id: number; relPath: string; text: string };
    rawMax: number;    // 原始 BM25 最高分
    normMax: number;   // 归一化 BM25 最高分
    queryHits: number; // 命中 query 次数
    rankScore: number; // 排名累计分（1 / (rank+1)）
}

// 小数保留 4 位：日志更可读，减少噪声
function roundScore(n: number): number {
    return Math.round(n * 10000) / 10000;
}

// 路径标准化：统一小写 + 去掉无关符号，便于做 includes 匹配
function normalizePathToken(s: string): string {
    return String(s || '')
        .toLowerCase()
        .replace(/[^a-z0-9._/-]+/g, ' ')
        .trim();
}

// 合并原始 query + 改写 query，去空值、去重、限长
function collectQueries(query: string, queryVariants: string[] = []): string[] {
    const arr = [query, ...queryVariants]
        .map(x => String(x || '').trim())
        .filter(Boolean);

    return [...new Set(arr)].slice(0, 4);
}

// 从 query 中提取路径提示词：
// 1) fullPathMatches: 带扩展名路径
// 2) segments: 路径上的各种文档名（含目录
function buildPathHints(queries: string[]): PathHints {
    const joined = queries.join(' ').toLowerCase();
    const fullPathMatches = joined.match(/[a-z0-9_./-]+\.[a-z0-9]+/g) || [];
    const segments = joined.match(/[a-z0-9_]{2,}/g) || [];

    return {
        fullPaths: new Set(fullPathMatches),
        segments: new Set(segments),
    };
}

// 路径加分：
// 1.直接命中完整路径 => 1.0
// 2.命中片段词按数量加分，最多 1.0
function calcPathBoost(relPath: string, hints: PathHints): number {
    const p = normalizePathToken(relPath);
    // 1.
    for (const full of hints.fullPaths) {
        if (full && p.includes(full)) return 1;
    }

    // 2.
    let hit = 0;
    for (const seg of hints.segments) {
        if (seg.length < 3) continue;
        if (p.includes(seg)) hit += 1;
    }

    if (!hit) return 0;
    return Math.min(1, hit / 3);
}

// 第一阶段召回：
// 1) indexProject 切 chunk
// 2) BM25 多 query 召回
// 3) 按多维信号融合分数
// 4) 返回 topK 候选
export function retrieveCandidatesByBm25({
    rootDir,
    files,
    query,
    queryVariants = [],
    topK = RAG_TOP_K,
    recallK = RAG_RECALL_K,
    stopWords = STOP_WORDS,
}: RetrieveInput): RetrievedHit[] {
    const chunks = indexProject({ rootDir, files });
    if (!chunks.length) return [];

    const index = buildBm25Index(chunks, stopWords);
    const queries = collectQueries(query, queryVariants);
    const merged = new Map<number, MergedStats>();

    for (const q of queries) {
        const scored = bm25Search(index, q, stopWords, recallK);

        // 当前 query 内的最高分：用于归一化（避免不同 query 分数尺度差异）
        const maxQScore = scored.reduce((m, x) => Math.max(m, x.score), 0);

        scored.forEach((item, rank) => {
            const id = item.doc.id;
            const prev = merged.get(id) || {
                doc: item.doc,
                rawMax: 0,
                normMax: 0,
                queryHits: 0,
                rankScore: 0,
            };

            const norm = maxQScore > 0 ? item.score / maxQScore : 0;

            prev.rawMax = Math.max(prev.rawMax, item.score);
            prev.normMax = Math.max(prev.normMax, norm);

            // score > 0 表示至少有词法命中
            if (item.score > 0) prev.queryHits += 1;

            // 排名越靠前，贡献越高：1, 1/2, 1/3...
            prev.rankScore += 1 / (rank + 1);

            merged.set(id, prev);
        });
    }

    const hints = buildPathHints(queries);
    const totalQueries = Math.max(1, queries.length); // 防止除零

    const list: RetrievedHit[] = [...merged.values()]
        .map(v => {
            const queryCoverage = v.queryHits / totalQueries;
            const rankNorm = Math.min(1, v.rankScore / totalQueries);
            const pathBoost = calcPathBoost(v.doc.relPath, hints);

            // 融合分：
            // 0.55 词法相关度
            // 0.25 query 覆盖率
            // 0.10 排名稳定性
            // 0.10 路径线索
            const fusedScore =
                0.55 * v.normMax +
                0.25 * queryCoverage +
                0.10 * rankNorm +
                0.10 * pathBoost;

            return {
                id: v.doc.id,
                relPath: v.doc.relPath,
                text: v.doc.text,
                score: roundScore(fusedScore),
                bm25Score: roundScore(v.rawMax),
                queryCoverage: roundScore(queryCoverage),
                pathBoost: roundScore(pathBoost),
            };
        })
        .sort((a, b) => b.score - a.score || b.bm25Score - a.bm25Score);

    // 优先保留“有明确信号”的候选：
    // BM25 命中 > 0 或 路径命中 > 0
    const positive = list.filter(x => x.bm25Score > 0 || x.pathBoost > 0);
    const picked = positive.length ? positive : list; // 兜底：避免结果全空
    return picked.slice(0, topK);
}

// 把 hits 打包成上下文字符串，控制总长度
export function buildContextFromHits(
    hits: Array<Pick<RetrievedHit, 'id' | 'relPath' | 'text' | 'score'>>,
    maxChars = RAG_READ_CHARS * 2,
): string {
    let used = 0;
    const sections: string[] = [];

    for (const h of hits) {
        const header = `--- CHUNK: ${h.relPath}#${h.id} (score=${h.score}) ---\n`;
        const remain = maxChars - used - header.length;
        if (remain <= 0) break;

        const raw = String(h.text || '');
        const clipped = raw.length > remain
            ? `${raw.slice(0, remain)}\n...<truncated>...`
            : raw;

        sections.push(`${header}${clipped}\n`);
        used += header.length + clipped.length + 1;
    }

    return sections.join('\n');
}

// 兼容旧用法：一步得到 hits + context
export function buildRagData({
    rootDir,
    files,
    query,
    queryVariants = [],
    topK = RAG_TOP_K,
}: BuildRagDataInput) {
    const hits = retrieveCandidatesByBm25({ rootDir, files, query, queryVariants, topK });
    const context = buildContextFromHits(hits);
    return { hits, context };
}
