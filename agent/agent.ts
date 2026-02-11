import fs from 'node:fs';
import path from 'node:path';

import {
    RAG_TOP_K,
    RAG_RECALL_K,
    RAG_RERANK_CANDIDATES,
    RAG_ENABLE_RERANK,
} from './config.js';
import { retrieveCandidatesByBm25, buildContextFromHits } from './retrieve.js';
import { buildPrompt } from './prompt.js';
import type { AgentMode, RetrievedHit } from './types.js';

// 模型接口配置（可由环境变量覆盖）
const API_BASE = process.env.LLM_BASE_URL || 'https://dashscope.aliyuncs.com/compatible-mode/v1';
const API_KEY = process.env.LLM_API_KEY;
const MODEL = process.env.LLM_MODEL || 'qwen3-coder-plus';

// 有效模式3种 + 扫描时忽略目录
const VALID_MODES = new Set<AgentMode>(['summary', 'code', 'chat']);
const IGNORE_DIRS = new Set(['node_modules', '.git', 'dist', 'build']);


// system: 全局指令
// user: 用户输入
// assistant: 模型历史输出
// 当前代码主要用 user + assistant

// ChatRole 消息角色
type ChatRole = 'user' | 'assistant' | 'system';

interface ChatMessage {
    role: ChatRole;
    content: string;
}

// OpenAI-compatible chat/completions 响应的最小结构
interface ChatCompletionResponse {
    choices?: Array<{
        message?: {
            content?: string;
        };
    }>;
}

interface CallChatContentArgs {
    messages: ChatMessage[];
    temperature?: number;
}

// 调用 JSON LLM 的统一返回：
// parsed = 解析成功的 JSON
// raw    = 原始字符串（解析失败时用于回退）
interface JsonLLMResult {
    parsed: unknown;
    raw: string;
}

interface RouteResult {
    mode: AgentMode;
    rewrittenQueries: string[];
}

interface CallLLMInput {
    userTask: string;
    rootDir: string;
    files: string[];
}

// 小数统一保留 4 位，便于日志阅读
function roundScore(n: number): number {
    return Math.round(n * 10000) / 10000;
}

// 限制在 [0, 1]，并处理 NaN
function clamp01(n: number): number {
    if (Number.isNaN(n)) return 0;
    return Math.max(0, Math.min(1, n));
}

// 运行时类型守卫：判断是不是普通对象
function isObject(v: unknown): v is Record<string, unknown> {
    return typeof v === 'object' && v !== null;
}

// 扫描目录并收集指定后缀文件
function scanFiles(rootDir: string, exts: string[] = ['.js', '.ts', '.tsx', '.json', '.md', '.txt']): string[] {
    const result: string[] = [];

    function walk(current: string): void {
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

    // 前置参数校验，报错更清晰
    if (!fs.existsSync(rootDir)) throw new Error(`path does not exist -> ${rootDir}`); // 是否存在
    if (!fs.statSync(rootDir).isDirectory()) throw new Error(`path is not a directory -> ${rootDir}`); // 是不是目录

    walk(rootDir);
    // 排序保证输出稳定，便于复现和调试
    result.sort();
    return result;
}

// 规则兜底模式判断（LLM route 失败时使用）
function inferModeHeuristic(userTask: string): AgentMode {
    const t = String(userTask || '').toLowerCase();

    if (/总结|summarize|summary|概览|overview|介绍/.test(t)) return 'summary';
    if (/改|修改|重构|修复|实现|新增|删除|代码|diff|patch|fix|refactor|implement|bug/.test(t)) return 'code';
    return 'chat';
}

// 清理并裁剪 query 列表：
// 1) 保留 userTask
// 2) 合并 rewrittenQueries
// 3) 去空值
// 4) 去重
// 5) 限制最大数量
function sanitizeQueries(userTask: string, rewrittenQueries: string[]): string[] {
    const q = [userTask, ...(rewrittenQueries || [])]
        .map(x => String(x || '').trim())
        .filter(Boolean)
        .slice(0, 6);

    return [...new Set(q)].slice(0, 4);
}

// 最底层模型调用：只负责返回 content 文本
async function callChatContent({ messages, temperature = 0.2 }: CallChatContentArgs): Promise<string> {
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

    const json = (await res.json()) as ChatCompletionResponse;
    const content = json?.choices?.[0]?.message?.content;
    if (!content) throw new Error('No content in LLM response');
    return content;
}

// 期望返回 JSON 的模型调用：
// 第一次解析失败时，自动二次修复提示
async function callJsonLLM({ messages, temperature = 0.2 }: CallChatContentArgs): Promise<JsonLLMResult> {
    const first = await callChatContent({ messages, temperature });

    try {
        return { parsed: JSON.parse(first), raw: first }; // 尝试第一次读取JSON
    } catch { // 如果第一次失败
        const repairPrompt = // 再来一次严格限制
`Your previous response was NOT valid JSON.
Return ONLY valid JSON, no markdown fences, no extra text.
Follow the required JSON schema strictly.`;

        const retryMessages: ChatMessage[] = [
            ...messages,
            { role: 'assistant', content: first },
            { role: 'user', content: repairPrompt },
        ];

        const second = await callChatContent({ messages: retryMessages, temperature: 0.0 });
        try {
            return { parsed: JSON.parse(second), raw: second }; // 第二次尝试解析
        } catch {
            // 第二次仍失败时返回原文，交给上层决定如何兜底
            return { parsed: null, raw: second };
        }
    }
}

// 路由层：让 LLM 同时完成两件事
// 1) 分类 mode（summary/code/chat）
// 2) 生成检索改写 query
async function routeTask(userTask: string): Promise<RouteResult> {
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

    const heuristicMode = inferModeHeuristic(userTask); // 用词类匹配得到mode

    try {
        const out = await callJsonLLM({
            messages: [{ role: 'user', content: routingPrompt }],
            temperature: 0.0,
        });

        const parsed = out.parsed;
        if (!isObject(parsed)) { // 没有得到JSON
            return {
                mode: heuristicMode,
                rewrittenQueries: sanitizeQueries(userTask, []),
            };
        }

        let mode: AgentMode = heuristicMode;
        if (typeof parsed.mode === 'string' && VALID_MODES.has(parsed.mode as AgentMode)) {
            mode = parsed.mode as AgentMode;
        }

        const rewritten = Array.isArray(parsed.rewritten_queries)
            ? parsed.rewritten_queries.filter((x): x is string => typeof x === 'string')
            : [];

        return {
            mode,
            rewrittenQueries: sanitizeQueries(userTask, rewritten),
        };
    } catch {
        // route 出错时走规则兜底，保证流程不中断
        return {
            mode: heuristicMode,
            rewrittenQueries: sanitizeQueries(userTask, []),
        };
    }
}

// 第二阶段重排：给候选 chunk 做 LLM relevance 打分
async function rerankCandidatesWithLLM(
    userTask: string,
    mode: AgentMode,
    candidates: RetrievedHit[],
): Promise<RetrievedHit[]> {
    if (!RAG_ENABLE_RERANK || !candidates.length) return candidates;

    // 先压缩候选字段，控制 prompt 成本
    const compactCandidates = candidates.map(c => ({
        id: c.id,
        relPath: c.relPath,
        lexical_score: c.score,
        snippet: String(c.text || '').slice(0, 260).replace(/\s+/g, ' '), // replace把 空格换行换成' '
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

    try {
        const out = await callJsonLLM({
            messages: [{ role: 'user', content: rerankPrompt }],
            temperature: 0.0,
        });

        if (!isObject(out.parsed)) return candidates;
        const rawScores = out.parsed.scores;
        if (!Array.isArray(rawScores)) return candidates;

        const scoreMap = new Map<number, number>();
        for (const s of rawScores) {
            if (!isObject(s)) continue;
            if (typeof s.id !== 'number') continue;
            scoreMap.set(s.id, clamp01(Number(s.score)));
        }

        // 融合 lexical 分与 LLM 分
        return candidates
            .map(c => {
                const llmScore = scoreMap.has(c.id) ? (scoreMap.get(c.id) as number) : c.score;
                const fused = 0.6 * c.score + 0.4 * llmScore;
                return {
                    ...c,
                    llmScore: roundScore(llmScore),
                    score: roundScore(fused),
                };
            })
            .sort((a, b) => b.score - a.score || b.bm25Score - a.bm25Score);
    } catch {
        // 重排失败不影响主流程，直接返回原候选
        return candidates;
    }
}

// 端到端主流程：route -> retrieve -> rerank -> prompt -> final LLM
async function callLLM({ userTask, rootDir, files }: CallLLMInput): Promise<string> {
    if (!API_BASE || !API_KEY) {
        throw new Error('Missing env: LLM_BASE_URL and/or LLM_API_KEY');
    }

    const { mode, rewrittenQueries } = await routeTask(userTask); // 判断模式并且重写query

    const candidates = retrieveCandidatesByBm25({ // 计算bm25分数
        rootDir,
        files,
        query: userTask,
        queryVariants: rewrittenQueries,
        topK: RAG_RERANK_CANDIDATES,
        recallK: RAG_RECALL_K,
    });

    const reranked = await rerankCandidatesWithLLM(userTask, mode, candidates); // 计算LLM分熟并且重排
    const hits = reranked.slice(0, RAG_TOP_K); // 只取前 RAG_TOP_K
    const context = buildContextFromHits(hits); // 用结果建立上下文

    // 打印检索日志，便于调试观察
    console.error('RAG mode:', mode);
    console.error('RAG queries:', rewrittenQueries.join(' | '));
    console.error('RAG hits:', hits.length ? hits.map(h => `${h.relPath}#${h.id}(${h.score})`).join(', ') : '(none)');

    const prompt = buildPrompt({ mode, userTask, hits, context }); // prompt
    const { parsed, raw } = await callJsonLLM({ // 最后调用LLM回答
        messages: [{ role: 'user', content: prompt }],
        temperature: 0.2,
    });

    if (parsed === null || parsed === undefined) return raw;
    return JSON.stringify(parsed, null, 2);
}

// 命令行入口
async function main(): Promise<void> {
    const args = process.argv.slice(2);

    if (args.length < 2) {
        console.error('Usage: node dist/agent.js <project_dir> "<task>"');
        console.error('Env: LLM_BASE_URL, LLM_API_KEY, (optional) LLM_MODEL');
        process.exit(1);
    }

    const rootDir = args[0] as string;
    const userTask = args.slice(1).join(' ');

    try {
        const files = scanFiles(rootDir);
        const answer = await callLLM({ userTask, rootDir, files });
        console.log(answer);
    } catch (e) {
        const msg = e instanceof Error ? e.message : String(e); // 是不是Error类型
        console.error('Error:', msg);
        process.exit(1);
    }
}

main();
