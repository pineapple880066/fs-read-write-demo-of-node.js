import { RAG_TOP_K, RAG_READ_CHARS, STOP_WORDS } from './config.js'
import { indexProject } from './indexer.js'
import { buildBm25Index, bm25Search } from './bm25.js'

function roundScore(n) {
    return Math.round(n * 10000) / 10000;
}

// 从项目里面检索最相关的 chunk (利用bm25)
export function retrieveByBm25({ rootDir, files, query, topK = RAG_TOP_K, stopWords = STOP_WORDS }) {
    const chunks = indexProject({ rootDir, files }); // 把每个file分成chunks 并且建立索引id

    if (!chunks.length) return [];

    const index = buildBm25Index(chunks, stopWords); // 建立索引 ， 计算所有chunks平均长度
    const results = bm25Search(index, query, stopWords, topK); // 计算bm25并且取 前topK个

    const postive = results.filter(item => item.score > 0);
    const picked = postive.length > 0 ? postive : results;
    return picked.map( ({ doc, score }) => ({
            id: doc.id,
            relPath: doc.relPath,
            text: doc.text,
            score: roundScore(score),
        }));
}

// 把命中的 chunks 拼成 prompt 上下文，并且控制总长度
    // hits是 检索命中的结果构成的数组 
    //         id: doc.id,
    //         relPath: doc.relPath,
    //         text: doc.text,
    //         score: roundScore(score),
export function buildContextFromHits(hits, maxChars = RAG_READ_CHARS * 2) {
    let used = 0; // 已使用的空间
    const sections = []; // 存放上下文

    for (const h of hits) {
        // 请求头 路径 id 分数(bm25)
        const header = `--- CHUNK: ${h.relPath}#${h.id} (score = ${h.score}) ---\n`;
        // 剩余空间
        const remain = maxChars - used - header.length;
        if (remain <= 0) break;

        const raw = String(h.text || ''); // 未处理的
        const clipped = raw.length > remain // 处理: 如果原文长度大于可用的，只取前remain个字符
            ? `${raw.slice(0, remain)}\n...<truncated>...`
            : raw;
        
        sections.push(`${header}${clipped}\n`); // 上下文加入 header , 处理过的chunk.text
        used += header.length + clipped.length + 1; // 已用量累加
    }

    return sections.join('\n'); // 用换行连接多个section 构成一个字符串
}

// 拿到检索结果 + context(调用前两个函数retrieveByBm25 + buildContextFromHits)
export function buildRagData({ rootDir, files, query, topK = RAG_TOP_K }) {
    const hits = retrieveByBm25({ rootDir, files, query, topK });
    const context = buildContextFromHits(hits);
    return { hits, context }; // 返回 带分数的最相关索引， 以及上下文context
}