const MODES = new Set(['summary', 'code', 'chat']); // 三种模式

 // 保证是三种模式之一，否则就是 chat 模式
function safeMode(mode) {
    return MODES.has(mode) ? mode : 'chat';
}

function formatHitList(hits) {  // 格式化处理hits结果(命中chunks)
    if (!hits.length) return '(none)';
    return hits
        .map((h, i) => `${i + 1}. ${h.relPath}#${h.id} (score=${h.score})`) // 每个命中的文件用固定格式给出
        .join('\n');
}

function formatFileList(hits) { // 处理file路径(去重)(命中file或者document)
    const uniq = [...new Set(hits.map(h => h.relPath))];
    if (!uniq.length) return '- (none)';
    return uniq.map(p => `- ${p}`).join('\n');
}

 // 保证 context 非空
function safeContext(context) {
    return context && context.trim() ? context : '(no retrieved context)'; // trim()去掉 context 首尾空格
}

// summary
function buildSummaryPrompt({ userTask, fileList, hitList, context }) {
    return `
You are a software analyst. Summarize strictly based on retrieved chunks.
Do NOT propose refactors or code edits unless explicitly requested.

User task:
${userTask}

Retrieved files:
${fileList}

Retrieved chunks:
${hitList}

Context:
${context}

Return ONLY valid JSON (no markdown, no extra text) with this schema:
{
  "summary": "what this project does (5-10 sentences, Chinese preferred)",
  "key_files": ["most important files (relative paths)"],
  "entrypoints": ["likely entry files (relative paths) or empty array if unknown"]
}
`;
}
// code
function buildCodePrompt({ userTask, fileList, hitList, context }) {
    return `
You are a coding assistant agent.
Goal: produce concrete code changes for the user task.

Hard constraints:
- Do NOT invent files that don't exist.
- Only modify files from the provided retrieved files.
- Return ONLY unified diffs for each file you change.
- Keep changes minimal and directly related to the user task.

User task:
${userTask}

Retrieved files:
${fileList}

Retrieved chunks:
${hitList}

Context:
${context}

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
// chat
function buildChatPrompt({ userTask, fileList, hitList, context }) {
    return `
You are a pragmatic engineering mentor.
Answer the user's question based on retrieved context. Do not output code diffs.
If evidence is insufficient, clearly say what is missing.

User task:
${userTask}

Retrieved files:
${fileList}

Retrieved chunks:
${hitList}

Context:
${context}

Return ONLY valid JSON (no markdown, no extra text) with this schema:
{
  "answer": "direct answer in Chinese",
  "evidence_files": ["relative/path.js"],
  "gaps": ["what is unknown or uncertain"],
  "next_steps": ["practical actions user can take"]
}
`;
}

export function buildPrompt({ mode, userTask, hits, context }) {
    const pickedMode = safeMode(mode);
    const hitList = formatHitList(hits || []);
    const fileList = formatFileList(hits || []);
    const packedContext = safeContext(context || '');

    if (pickedMode === 'summary') {
        return buildSummaryPrompt({ userTask, fileList, hitList, context: packedContext });
    }

    if (pickedMode === 'code') {
        return buildCodePrompt({ userTask, fileList, hitList, context: packedContext });
    }

    return buildChatPrompt({ userTask, fileList, hitList, context: packedContext });
}
