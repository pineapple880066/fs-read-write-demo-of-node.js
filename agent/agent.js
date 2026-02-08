import fs from 'fs';
import path from 'path';

import { RAG_TOP_K } from './config.js';
import { buildRagData } from './retrieve.js';
import { buildPrompt } from './prompt.js';

// 模型 API 配置（可通过环境变量覆盖）
const API_BASE = process.env.LLM_BASE_URL || 'https://dashscope.aliyuncs.com/compatible-mode/v1';
const API_KEY = process.env.LLM_API_KEY;
const MODEL = process.env.LLM_MODEL || 'qwen3-coder-plus';

// 扫描目录时忽略的目录名
const IGNORE_DIRS = new Set(['node_modules', '.git', 'dist', 'build']);

// 递归扫描目录，收集指定后缀文件
function scanFiles(rootDir, exts = ['.js', '.ts', '.tsx', '.json', '.md', '.txt']) {
    const result = [];

    function walk(current) {
        const entries = fs.readdirSync(current);

        for (const entry of entries) {
            if (IGNORE_DIRS.has(entry)) continue;

            // 把“当前目录 + 子项名”拼成完整路径
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
    // 默认字典序排序，保证扫描结果稳定
    result.sort();
    return result;
}

// 调用 LLM 并返回 JSON 字符串
async function callLLM({ userTask, rootDir, files }) {
    if (!API_BASE || !API_KEY) {
        throw new Error('Missing env: LLM_BASE_URL and/or LLM_API_KEY');
    }

    // RAG：检索相关 chunks，并拼装上下文
    const { hits, context } = buildRagData({
        rootDir,
        files,
        query: userTask,
        topK: RAG_TOP_K,
    });

    console.error(
        'RAG hits:',
        hits.length ? hits.map(h => `${h.relPath}#${h.id}(${h.score})`).join(', ') : '(none)'
    );

    const prompt = buildPrompt({ userTask, hits, context });

    const res = await fetch(`${API_BASE}/chat/completions`, {
        method: 'post',
        headers: {
            Authorization: `Bearer ${API_KEY}`,
            'Content-Type': 'application/json',
        },
        body: JSON.stringify({
            model: MODEL,
            messages: [{ role: 'user', content: prompt }],
            temperature: 0.2,
        }),
    });

    if (!res.ok) {
        const t = await res.text();
        throw new Error(`LLM HTTP ${res.status}: ${t}`);
    }

    const json = await res.json();
    const content = json?.choices?.[0]?.message?.content;
    if (!content) throw new Error('No content in LLM response');

    let parsed = null;
    try {
        parsed = JSON.parse(content);
    } catch (_) {
        const repairPrompt =
        `Your previous response was NOT valid JSON.
Return ONLY valid JSON, no markdown fences, no extra text.
Follow the required JSON schema strictly.`;

        const retryRes = await fetch(`${API_BASE}/chat/completions`, {
            method: 'post',
            headers: {
                Authorization: `Bearer ${API_KEY}`,
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({
                model: MODEL,
                messages: [
                    { role: 'user', content: prompt },
                    { role: 'assistant', content: content },
                    { role: 'user', content: repairPrompt },
                ],
                temperature: 0.0,
            }),
        });

        if (!retryRes.ok) {
            const tt = await retryRes.text();
            throw new Error(`LLM HTTP ${retryRes.status}: ${tt}`);
        }

        const retryJson = await retryRes.json();
        const retryContent = retryJson?.choices?.[0]?.message?.content;
        if (!retryContent) throw new Error('No content in LLM retry response');

        try {
            parsed = JSON.parse(retryContent);
        } catch (_) {
            // 第二次仍不是 JSON，原样返回便于调试
            return retryContent;
        }
    }

    // 缩进 2 空格，便于阅读
    return JSON.stringify(parsed, null, 2);
}

async function main() {
    const args = process.argv.slice(2);
    if (args.length < 2) {
        console.error('Usage: node agent.js <project_dir> "<task>" ');
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
