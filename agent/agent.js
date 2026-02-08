import fs from 'fs';
import path from 'path';

// 模型 API 配置（默认走通义兼容接口）
const API_BASE = process.env.LLM_BASE_URL || "https://dashscope.aliyuncs.com/compatible-mode/v1";
const API_KEY  = process.env.LLM_API_KEY;
const MODEL = process.env.LLM_MODEL || "qwen3-coder-plus";

// RAG配置
const RAG_TOP_K = Number(process.env.RAG_TOP_K || 8);
const RAG_READ_CHARS = Number(process.env.RAG_READ_CHARS || 4000);
const STOP_WORDS = new Set(['的', '了', '和', '是', '在', '我', '要', '把', 'to', 'the', 'a', 'an', 'for', 'and', 'or', 'is', 'are']);

// 最小扫描: (递归 + ignore + ext)
const IGNORE_DIRS = new Set(['node_modules', '.git', 'dist', 'build']);

function scanFiles(rootDir, exts = ['.js', '.ts', '.tsx', '.json', '.md', '.txt']) {
    const result = [];
    // walk只负责递归扫描目录
    function walk(current) {
        const entries = fs.readdirSync(current);

        for (const entry of entries) {
            if (IGNORE_DIRS.has(entry)) { continue; }

            const full = path.join(current, entry);
            const st = fs.statSync(full);
            if (st.isDirectory()) {
                walk(full);
            } else {
                const ext = path.extname(entry);
                // 只采样指定后缀，避免把二进制/大文件塞进prompt
                if (ext && exts.includes(ext)) { result.push(full); }
            }
        }
    }

    // 先做参数校验，避免递归时抛出更难理解的异常
    if (!fs.existsSync(rootDir)) { throw new Error('path does not exist -> ' + rootDir); }
    if (!fs.statSync(rootDir).isDirectory()) { throw new Error('path is not a directory -> ' + rootDir); }
    
    walk(rootDir);
    result.sort();
    return result;
}

// 读取文本文件，限制最大字符数，防止过大文件读取(如果超过6000字符就截断)
function readTextSafe(filePath, maxChars = 6000) {
    const buf = fs.readFileSync(filePath);
    const text = buf.toString("utf-8");
    if (text.length <= maxChars) {
        return text;
    } else {
        // 超出部分直接截断，避免prompt过长导致请求失败
        return text.slice(0, maxChars) + '\n\n...<truncated>...'; // ...被截断的...
    }
}

function tokenize(text) {
    return String(text).toLowerCase().split(/[^a-z0-9_\u4e00-\u9fa5]+/.filter(t => t && !STOP_WORDS.has(t) && t.length > 1));
}

function scoreFileByTask(task, relPath, contentHead) {
    const q = tokenize(task);
    const pathTokens = tokenize(relPath);
    const bodyTokens = tokenize(contentHead);

    let score = 0;
    for (const t of q) {
        if (pathTokens.includes(t)) score += 3;
        if (bodyTokens.includes(t)) score += 1;
    }
    if (/readme|index|main|app/i.test(relPath)) score += 0.5;
    return score;
}

function pickRelevantFiles({ rootDir, files, userTask, topK = RAG_TOP_K }) {
    const scored = files.map(fp => {
        const rel = path.relative(rootDir, fp);
        const head = readTextSafe(fp, 1200);
        return { fp, rel, score: scoreFileByTask(userTask, rel, head) };
    }).sort((a, b) => b.score - a.score);
    const picked = scored.filter(x => x.score > 0).slice(0, topK);
    return picked.length ? picked : scored.slice(0, topK);
}
// 提示词的建立
function buildPrompt({ userTask, rootDir, files, picked, fileBlobs }) {
    // 用户的prompt里面出现总结或者summary的时候，切换总结模式
    const isSummary = /总结|summarize/i.test(userTask); 

    if (isSummary) {
        // 总结模式：只输出JSON摘要，不给改代码建议
        return `
You are a software analyst. Summarize the project strictly based on the provided file list and contents.
Do NOT propose refactors or code edits unless the user explicitly asks to change code.

Project root: ${rootDir}
Known files (sampled ${picked.length}/${files.length}):
${picked.map(fp => "- " + path.relative(rootDir, fp)).join("\n")}

User task:
${userTask}

File contents:
${fileBlobs}

Return ONLY valid JSON (no markdown, no extra text) with this schema:
{
  "summary": "what this project does (5-10 sentences, Chinese preferred)",
  "key_files": ["most important files (relative paths)"],
  "entrypoints": ["likely entry files (relative paths) or empty array if unknown"]
}
`;
    }

    // 改代码模式：要求输出plan + diffs 的JSON
    return `
You are a coding assistant agent.
Goal: produce concrete code changes for the user task.

Hard constraints:
- Do NOT invent files that don't exist.
- Only modify files from the provided list.
- Return ONLY unified diffs for each file you change.
- Keep changes minimal and directly related to the user task.

Project root: ${rootDir}
Known files (sampled ${picked.length}/${files.length}):
${picked.map(fp => "- " + path.relative(rootDir, fp)).join("\n")}

User task:
${userTask}

File contents:
${fileBlobs}

Return ONLY valid JSON (no markdown, no extra text) with this schema:
{
  "plan": ["step1", "step2", "..."],
  "diffs": [
    {
      "path": "relative/path/to/file.js",
      "unified_diff": "diff --git a/... b/...\\n..."
    }
  ]
}
If no changes are needed, return:
{ "plan": ["no changes"], "diffs": [] }
`;
}

// LLM调用
async function callLLM({ userTask, rootDir, files }) {
    // 这里必须有 API_BASE 和 API_KEY，否则无法请求
    if (!API_BASE || !API_KEY) {
        throw new Error('Missing env: LLM_BASE_URL and/or LLM_API_KEY');
    }

    // 先限制为只喂最大20个文件 // 之后再改为相关性挑选(已改)
    const MAX_FILES = 20;
    const pickedItems = pickRelevantFiles({ rootDir, files, userTask, topK: RAG_TOP_K });
    const picked = pickedItems.map(x => x.fp);

    const fileBlobs = picked.map((fp) => {
        const rel = path.relative(rootDir, fp); // 只用相对路径，减少token
        const content = readTextSafe(fp, RAG_READ_CHARS); // 读取字符上限改为RAG_READ_CHARS，防止prompt爆炸

        // 统一拼成“文件头+内容”的格式，便于模型引用
        return `--- FILE: ${rel} ---\n${content}\n`; // 返回的格式:文件的相对路径 + 文件内容
    }).join('\n');
    
    console.error('RAG picked:', pickedItems.map(x => `${x.rel}(${x.score})`).join(', '));

    // 设置提示词（根据任务自动切换：总结模式 / 改代码模式）
    const prompt = buildPrompt({ userTask, rootDir, files, picked, fileBlobs });

    // 等待获取模型输出
    const res = await fetch(`${API_BASE}/chat/completions`, {
        method: 'post',
        headers: {
            'Authorization': `Bearer ${API_KEY}`,
            'Content-Type': 'application/json',
        },
        body: JSON.stringify({
            model: MODEL,
            messages: [
                { role: "user", content: prompt }
            ],
            temperature: 0.2, // 
        }),
    });

    if (!res.ok) {
        const t = await res.text();
        throw new Error(`LLM HTTP ${res.status}: ${t}`);
    }

    const json = await res.json();
    const content = json?.choices?.[0]?.message?.content;
    if (!content) { throw new Error('No content in LLM response'); }

    // 尝试解析 JSON；如果失败，自动重试一次，强制模型只输出 JSON（不带 markdown）
    let parsed = null;
    try {
        parsed = JSON.parse(content);
    } catch (_) {
        // 第二次调用：给一个更强硬的修复提示
        const repairPrompt =
`Your previous response was NOT valid JSON.
Return ONLY valid JSON, no markdown fences, no extra text.
Follow the required JSON schema strictly.`;

        const retryRes = await fetch(`${API_BASE}/chat/completions`, {
            method: 'post',
            headers: {
                'Authorization': `Bearer ${API_KEY}`,
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({
                model: MODEL,
                messages: [
                    { role: "user", content: prompt },
                    { role: "assistant", content: content },
                    { role: "user", content: repairPrompt }
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
        if (!retryContent) { throw new Error('No content in LLM retry response'); }

        try {
            parsed = JSON.parse(retryContent);
        } catch (_) {
            // 第二次仍非 JSON，就原样返回便于你调试
            return retryContent;
        }
    }

    // 统一格式化输出，便于人类阅读/下游解析
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