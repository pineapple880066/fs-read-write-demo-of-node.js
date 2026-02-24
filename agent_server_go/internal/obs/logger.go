package obs

import (
	"log"
	"os"
)

type Logger struct {
	// 直接嵌入标准库 logger，方便复用 Printf 等方法
	*log.Logger
}

func NewLogger() *Logger {
	// 统一日志前缀，方便本地多服务联调时快速识别来源
	return &Logger{Logger: log.New(os.Stdout, "[agent-server] ", log.LstdFlags|log.Lmicroseconds)}
}
