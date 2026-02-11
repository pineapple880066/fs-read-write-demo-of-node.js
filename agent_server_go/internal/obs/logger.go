package obs

import (
	"log"
	"os"
)

type Logger struct {
	*log.Logger
}

func NewLogger() *Logger {
	return &Logger{Logger: log.New(os.Stdout, "[agent-server] ", log.LstdFlags|log.Lmicroseconds)}
}
