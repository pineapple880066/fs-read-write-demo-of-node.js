//       文件选取数量  最大字符读取数量   重召回数量      停止词
import { RAG_TOP_K, RAG_READ_CHARS, RAG_RECALL_K, STOP_WORDS } from './config.js';
//        建立chunks的索引
import { indexProject } from './indexer.js';
//        建立bm25索引，并且返回平均值     计算bm25并且绑定chunk
import { buildBm25Index,               bm25Search } from './bm25.js';

function roundScore(n) { // 分数保留到小数点后4位即可
    return Math.round(n * 10000) / 10000;
}

function normalizePathToken(s) {
    return String(s || '') // 防止为空字符
        .toLowerCase() // 全部转小写
        .replace(/[^a-z0-9._/-]+/g, ' ') // 不是a-z,0-9,_/-的字符全部替换为空格
        .trim();
}

function collectQueries(query, queryVariants) {
    const arr = [query, ...(queryVariants || [])]
        .map(x => String(x || '').trim()) // 去掉空字符以及前后的空格
        .filter(Boolean);

    return [...new Set(arr)].slice(0, 4); // 去重
}

function buildPathHints(queries) {
    const joined = queries.join(' ').toLowerCase();
    const fullPathMatches = joined.match(/[a-z0-9_./-]+\.[a-z0-9]+/g) || [];
    const segments = joined.match(/[a-z0-9_]{2,}/g) || [];

    return {
        fullPaths: new Set(fullPathMatches),
        segments: new Set(segments),
    };
}

function calcPathBoost(relPath, hints) {
    const p = normalizePathToken(relPath);

    for (const full of hints.fullPaths) {
        if (full && p.includes(full)) return 1;
    }

    let hit = 0;
    for (const seg of hints.segments) {
        if (seg.length < 3) continue;
        if (p.includes(seg)) hit += 1;
    }

    if (!hit) return 0;
    return Math.min(1, hit / 3);
}

// 第一阶段召回：BM25 多查询召回 + 分数融合
export function retrieveCandidatesByBm25({
    rootDir,
    files,
    query,
    queryVariants = [],
    topK = RAG_TOP_K,
    recallK = RAG_RECALL_K,
    stopWords = STOP_WORDS,
}) {
    const chunks = indexProject({ rootDir, files });
    if (!chunks.length) return [];

    const index = buildBm25Index(chunks, stopWords);
    const queries = collectQueries(query, queryVariants);
    const merged = new Map();

    for (const q of queries) {
        const scored = bm25Search(index, q, stopWords, recallK);
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

            const norm = maxQScore > 0 ? (item.score / maxQScore) : 0;
            prev.rawMax = Math.max(prev.rawMax, item.score);
            prev.normMax = Math.max(prev.normMax, norm);
            if (item.score > 0) prev.queryHits += 1;
            prev.rankScore += 1 / (rank + 1);
            merged.set(id, prev);
        });
    }

    const hints = buildPathHints(queries);
    const totalQueries = Math.max(1, queries.length);

    const list = [...merged.values()]
        .map(v => {
            const queryCoverage = v.queryHits / totalQueries;
            const rankNorm = Math.min(1, v.rankScore / totalQueries);
            const pathBoost = calcPathBoost(v.doc.relPath, hints);
            const fusedScore = 0.55 * v.normMax + 0.25 * queryCoverage + 0.10 * rankNorm + 0.10 * pathBoost;

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

    const positive = list.filter(x => x.bm25Score > 0 || x.pathBoost > 0);
    const picked = positive.length ? positive : list;
    return picked.slice(0, topK);
}

// 把命中的 chunks 拼成 prompt 上下文，并控制总长度
export function buildContextFromHits(hits, maxChars = RAG_READ_CHARS * 2) {
    let used = 0;
    const sections = [];

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

// 兼容旧调用：一步返回 hits + context
export function buildRagData({ rootDir, files, query, queryVariants = [], topK = RAG_TOP_K }) {
    const hits = retrieveCandidatesByBm25({ rootDir, files, query, queryVariants, topK });
    const context = buildContextFromHits(hits);
    return { hits, context };
}
