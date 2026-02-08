function formatHitList(hits) { // 把hits(检索命中结果)整理出来
    if (!hits.length) return '(none)';
    return hits
        //               下标加一    相对路径      id               分数  
        .map((h, i) => `${i + 1}.${h.relPath}#${h.id}(score=${h.score})`)
        .join('\n');
}

function formatFileList(hits) { // 整理出文档(file)
    const uniq = [...new Set(hits.map(h => h.relPath))]; // 取出所有relPath, ...Set去重后再放入数组uniq
    if (!uniq.length) return '- (none)'; // 
    return uniq.map(p => `- ${p}`).join('\n'); // 变为 ‘- ${relPath1}\n- ${relPath2}’的形式
}

export function buildPrompt({ userTask, hits, context }) {
    const isSummary = /总结|summarize|summary/i.test(userTask); // 正则表达式看看是不是总结类型的,不分大小写
    const hitList = formatHitList(hits);
    const fileList = formatFileList(hits);
    
    if (isSummary) {
        return`
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

    // 不是总结的情况:
    return  `
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
