package errors

import "fmt"

type AppError struct {
	// Code 用于机器识别，Message 用于人类阅读
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e AppError) Error() string {
	// 实现 error 接口，便于在 Go 标准错误链中使用
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func New(code, message string) AppError {
	// 当前项目里用得不多，后续可统一替换 string error
	return AppError{Code: code, Message: message}
}
