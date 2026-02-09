// 文本块的长度 （值大， 上下文更长， 值小，颗粒更细）
export const CHUNK_SIZE = Number(process.env.CHUNK_SIZE || 800);
// 相邻块的重叠长度（一个概念不会被刚好切断）
export const CHUNK_OVERLAP = Number(process.env.CHUNK_OVERLAP || 120);
// 检索阶段最多取多少个 相关块/文件
export const RAG_TOP_K = Number(process.env.RAG_TOP_K || 8);
// 第一阶段召回数量（通常比 RAG_TOP_K 大）
export const RAG_RECALL_K = Number(process.env.RAG_RECALL_K || 40);
// 参与 LLM 重排的候选数量
export const RAG_RERANK_CANDIDATES = Number(process.env.RAG_RERANK_CANDIDATES || 20);
// 是否启用 LLM 重排（1 开启，0 关闭）
export const RAG_ENABLE_RERANK = String(process.env.RAG_ENABLE_RERANK || '1') !== '0';
// 读取文件的最大字符上限
export const RAG_READ_CHARS = Number(process.env.RAG_READ_CHARS || 4000);
// 过滤的字符，减少token
export const STOP_WORDS = new Set([
    '的','了','和','是','在','我','要','把',
    'to','the','a','an','for','and','or','is','are'
])
