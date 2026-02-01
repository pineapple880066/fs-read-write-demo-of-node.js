# demo.js — 目录扫描小工具
(gitignore 是忽略的其他文件)
简短说明：这是一个基于 Node.js 的小脚本，用于递归扫描指定目录，收集所有后缀为 `.js` 的文件路径，并把结果写入 `result.txt`（每行一个路径）。

位置：根目录 `demo.js`

主要功能
- 递归遍历指定目录及其子目录（深度优先）。
- 仅收集以 `.js` 结尾的文件路径。
- 将收集到的路径写入当前工作目录下的 `result.txt`（覆盖写入）。

用法
```bash
# 默认扫描 ./text（若不存在会报错）
node demo.js

# 指定要扫描的目录，例如扫描当前仓库的 file_nodejs_demo：
node demo.js ./file_nodejs_demo
```

输出
- 脚本成功运行后，会在运行目录生成或覆盖 `result.txt`，内容为找到的每个 `.js` 文件的绝对或相对路径（每行一条）。

注意事项与建议
- 确保传入的目录存在并有读取权限。脚本使用同步 API（`fs.readdirSync` / `fs.statSync`），适合小型目录遍历，若要处理非常大的目录建议改为异步实现以避免阻塞。 
- 当前脚本以 `.js` 后缀作为过滤条件；若需扩展为其他类型，请修改 `entry.endsWith(".js")` 条件或增加参数支持。
- 运行脚本时的当前工作目录会决定 `result.txt` 的输出位置。

Node 要求
- 推荐使用 Node.js 14+（脚本使用了 ES 模块导入语法 `import`，在某些 Node 版本需在 `package.json` 中设置 `"type": "module"`，或把 `import` 改为 CommonJS 的 `require`）。

举例
```bash
# 在仓库根运行并把 file_nodejs_demo 下的 .js 文件写入 result.txt
node demo.js ./file_nodejs_demo

# 查看结果 或者 直接点开
cat result.txt
```

许可证：MIT
