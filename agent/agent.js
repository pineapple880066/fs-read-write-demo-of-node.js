import fs from 'fs';
import path from 'path';

import {
    RAG_TOP_K,
    RAG_RECALL_K,
    RAG_RERANK_CANDIDATES,
    RAG_ENABLE_RERANK,
} from './config.js';
import { retrieveCandidatesByBm25, buildContextFromHits } from './retrieve.js';
import { buildPrompt } from './prompt.js';

// 模型 API 配置（可通过环境变量覆盖）
const API_BASE = process.env.LLM_BASE_URL || 'https://dashscope.aliyuncs.com/compatible-mode/v1';
const API_KEY = process.env.LLM_API_KEY;
const MODEL = process.env.LLM_MODEL || 'qwen3-coder-plus';

// 设置三种 agent 模式
const VALID_MODES = new Set(['summary', 'code', 'chat']);

// 扫描目录时忽略的目录名
const IGNORE_DIRS = new Set(['node_modules', '.git', 'dist', 'build']);

function roundScore(n) {
    return Math.round(n * 10000) / 10000;
}

function clamp01(n) {
    if (Number.isNaN(n)) return 0; // 防止除零异常
    return Math.max(0, Math.min(1, n)); // 归一化？？
}

// 递归扫描目录，收集指定后缀文件
function scanFiles(rootDir, exts = ['.js', '.ts', '.tsx', '.json', '.md', '.txt']) {
    const result = [];

    function walk(current) {
        const entries = fs.readdirSync(current);

        for (const entry of entries) {
            if (IGNORE_DIRS.has(entry)) continue;

            const full = path.join(current, entry);
            const st = fs.statSync(full);

            if (st.isDirectory()) {
                walk(full);
            } else {
                const ext = path.extname(entry);
                if (ext && exts.includes(ext)) result.push(full);
            }
        }
    }

    if (!fs.existsSync(rootDir)) throw new Error('path does not exist -> ' + rootDir);
    if (!fs.statSync(rootDir).isDirectory()) throw new Error('path is not a directory -> ' + rootDir);

    walk(rootDir);
    result.sort();
    return result;
}

function inferModeHeuristic(userTask) {
    const t = String(userTask || '').toLowerCase();

    if (/总结|summarize|summary|概览|overview|介绍/.test(t)) return 'summary';
    if (/改|修改|重构|修复|实现|新增|删除|代码|diff|patch|fix|refactor|implement|bug/.test(t)) return 'code';
    return 'chat';
}

function sanitizeQueries(userTask, rewrittenQueries) { // 清理查询
    const q = [userTask, ...(rewrittenQueries || [])] // ??
        .map(x => String(x || '').trim())
        .filter(Boolean)
        .slice(0, 6);

    return [...new Set(q)].slice(0, 4);
}

async function callChatContent({ messages, temperature = 0.2 }) {
    const res = await fetch(`${API_BASE}/chat/completions`, {
        method: 'post',
        headers: {
            Authorization: `Bearer ${API_KEY}`,
            'Content-Type': 'application/json',
        },
        body: JSON.stringify({
            model: MODEL,
            messages,
            temperature,
        }),
    });

    if (!res.ok) {
        const t = await res.text();
        throw new Error(`LLM HTTP ${res.status}: ${t}`);
    }

    const json = await res.json();
    const content = json?.choices?.[0]?.message?.content;
    if (!content) throw new Error('No content in LLM response');
    return content;
}

async function callJsonLLM({ messages, temperature = 0.2 }) {
    const first = await callChatContent({ messages, temperature });

    try {
        return { parsed: JSON.parse(first), raw: first };
    } catch (_) {
        const repairPrompt =
`Your previous response was NOT valid JSON.
Return ONLY valid JSON, no markdown fences, no extra text.
Follow the required JSON schema strictly.`;

        const retryMessages = [
            ...messages,
            { role: 'assistant', content: first },
            { role: 'user', content: repairPrompt },
        ];

        const second = await callChatContent({ messages: retryMessages, temperature: 0.0 });
        try {
            return { parsed: JSON.parse(second), raw: second };
        } catch (_) {
            return { parsed: null, raw: second };
        }
    }
}

async function routeTask({ userTask }) {
    const routingPrompt = `
Classify the user task into one of: summary, code, chat.
Also rewrite the task into 1-3 concise retrieval queries for source-code search.

User task:
${userTask}

Return ONLY valid JSON with this schema:
{
  "mode": "summary|code|chat",
  "rewritten_queries": ["query1", "query2"]
}
`;

    const heuristicMode = inferModeHeuristic(userTask);
    let parsed = null;

    try {
        const out = await callJsonLLM({
            messages: [{ role: 'user', content: routingPrompt }],
            temperature: 0.0,
        });
        parsed = out.parsed;
    } catch (_) {
        return {
            mode: heuristicMode,
            rewrittenQueries: sanitizeQueries(userTask, []),
        };
    }

    let mode = heuristicMode;
    let rewrittenQueries = [userTask];

    if (parsed && typeof parsed === 'object') {
        if (VALID_MODES.has(parsed.mode)) {
            mode = parsed.mode;
        }

        if (Array.isArray(parsed.rewritten_queries)) {
            rewrittenQueries = sanitizeQueries(userTask, parsed.rewritten_queries);
        } else {
            rewrittenQueries = sanitizeQueries(userTask, []);
        }
    }

    return { mode, rewrittenQueries };
}

async function rerankCandidatesWithLLM({ userTask, mode, candidates }) {
    if (!RAG_ENABLE_RERANK || !candidates.length) return candidates;

    const compactCandidates = candidates.map(c => ({
        id: c.id,
        relPath: c.relPath,
        lexical_score: c.score,
        snippet: String(c.text || '').slice(0, 260).replace(/\s+/g, ' '),
    }));

    const rerankPrompt = `
You are a retrieval reranker.
Given a task and candidate chunks, score each candidate relevance between 0 and 1.
Task mode: ${mode}
Task: ${userTask}

Candidates:
${JSON.stringify(compactCandidates, null, 2)}

Return ONLY valid JSON with this schema:
{
  "scores": [
    { "id": 1, "score": 0.92, "reason": "short reason" }
  ]
}
Rules:
- Only include ids from the provided candidates.
- score must be between 0 and 1.
- Higher means more relevant.
`;

    let parsed = null;
    try {
        const out = await callJsonLLM({
            messages: [{ role: 'user', content: rerankPrompt }],
            temperature: 0.0,
        });
        parsed = out.parsed;
    } catch (_) {
        return candidates;
    }

    if (!parsed || !Array.isArray(parsed.scores)) return candidates;

    const scoreMap = new Map();
    for (const s of parsed.scores) {
        if (!s || typeof s.id !== 'number') continue;
        scoreMap.set(s.id, clamp01(Number(s.score)));
    }

    return candidates
        .map(c => {
            const llmScore = scoreMap.has(c.id) ? scoreMap.get(c.id) : c.score;
            const fused = 0.6 * c.score + 0.4 * llmScore;
            return {
                ...c,
                llmScore: roundScore(llmScore),
                score: roundScore(fused),
            };
        })
        .sort((a, b) => b.score - a.score || b.bm25Score - a.bm25Score);
}

// 调用 LLM 并返回 JSON 字符串
async function callLLM({ userTask, rootDir, files }) {
    if (!API_BASE || !API_KEY) {
        throw new Error('Missing env: LLM_BASE_URL and/or LLM_API_KEY');
    }

    const { mode, rewrittenQueries } = await routeTask({ userTask });

    const candidates = retrieveCandidatesByBm25({
        rootDir,
        files,
        query: userTask,
        queryVariants: rewrittenQueries,
        topK: RAG_RERANK_CANDIDATES,
        recallK: RAG_RECALL_K,
    });

    const reranked = await rerankCandidatesWithLLM({ userTask, mode, candidates });
    const hits = reranked.slice(0, RAG_TOP_K);
    const context = buildContextFromHits(hits);

    console.error('RAG mode:', mode);
    console.error('RAG queries:', rewrittenQueries.join(' | '));
    console.error(
        'RAG hits:',
        hits.length ? hits.map(h => `${h.relPath}#${h.id}(${h.score})`).join(', ') : '(none)'
    );

    const prompt = buildPrompt({ mode, userTask, hits, context });
    const { parsed, raw } = await callJsonLLM({
        messages: [{ role: 'user', content: prompt }],
        temperature: 0.2,
    });

    if (!parsed) return raw;
    return JSON.stringify(parsed, null, 2);
}

async function main() {
    const args = process.argv.slice(2);

    if (args.length < 2) {
        console.error('Usage: node agent.js <project_dir> "<task>"');
        console.error('Env: LLM_BASE_URL, LLM_API_KEY, (optional) LLM_MODEL');
        process.exit(1);
    }

    const rootDir = args[0];
    const userTask = args.slice(1).join(' ');

    try {
        const files = scanFiles(rootDir);
        const answer = await callLLM({ userTask, rootDir, files });
        console.log(answer);
    } catch (e) {
        console.error('Error:', e?.message || String(e));
        process.exit(1);
    }
}

main();
