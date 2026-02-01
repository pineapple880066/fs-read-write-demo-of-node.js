import fs from 'fs';
import path from 'path';

console.log("progeam start"); 

function main() {
    const args = process.argv;
    const dir = args[2] || "./text";
    scanDir(dir);
}

function scanDir(dir) { // 读出目录,并写入 result.txt
    let result = [];
    // 之前demo用来测试的代码
    // for (const f of files) { 
    //     if (f.endsWith(".js")) {
    //         output += f + "\n";
    //     }
    // }
    function walk(currentDir) {
        const entries = fs.readdirSync(currentDir);

        for (const entry of entries) {
            const fullPath = path.join(currentDir, entry);
            const stat = fs.statSync(fullPath);

            if (stat.isDirectory()) { // 如果是目录，递归读取(相当于深度优先遍历)
                walk(fullPath); // 递归读取子目录
            }
            else if (entry.endsWith(".js")) {
                result.push(fullPath); // 如果后缀是.js ,路径写入result数组
            }
        }
    }
    
    // fs.writeFileSync("result.txt", output, "utf-8"); // 测试
    walk(dir) // 执行
    
    fs.writeFileSync("result.txt", result.join("\n"), "utf-8"); // 写入result.txt，result.join("\n")把数组变成字符串
    console.log("result written to result.txt");
}


main();