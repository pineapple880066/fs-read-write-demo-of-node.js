// 向量检索预留模块：
// 当前主流程是 BM25 + rerank，后续可在这里接入 embedding provider。

export interface EmbedResult {
    vector: number[];
    model: string;
}

// 占位函数：后续可接 OpenAI / DashScope / 本地模型
export async function embedText(_text: string): Promise<EmbedResult> {
    throw new Error('embedText is not implemented yet');
}
