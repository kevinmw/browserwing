package logger

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Module 定义日志模块类型
type Module string

const (
	ModuleSystem  Module = "system"  // 系统启动、关闭等
	ModuleAPI     Module = "api"     // HTTP API 请求
	ModuleBrowser Module = "browser" // 浏览器操作
	ModuleScript  Module = "script"  // 脚本执行
	ModuleAgent   Module = "agent"   // Agent 任务
	ModuleLLM     Module = "llm"     // LLM 调用
	ModuleMCP     Module = "mcp"     // MCP 协议
	ModuleTask    Module = "task"    // 定时任务
)

// Logger 日志接口
type Logger interface {
	Warn(ctx context.Context, msg string, args ...any)
	Error(ctx context.Context, msg string, args ...any)
	Info(ctx context.Context, msg string, args ...any)
	Debug(ctx context.Context, msg string, args ...any)
	WithContext(ctx context.Context) Logger
	WithModule(module Module) Logger
	WithField(key string, value interface{}) Logger
	WithFields(fields map[string]interface{}) Logger
}

type logrusLogger struct {
	logger *logrus.Entry
}

// getCallerFunctionName 获取调用者的函数名
func getCallerFunctionName() string {
	pc := make([]uintptr, 10)
	runtime.Callers(3, pc)
	funcName := runtime.FuncForPC(pc[0]).Name()
	// 提取最后一个点之后的部分作为函数名
	parts := strings.Split(funcName, ".")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "unknown"
}

func (l *logrusLogger) WithModule(module Module) Logger {
	return &logrusLogger{logger: l.logger.WithField("module", module)}
}

func (l *logrusLogger) WithField(key string, value interface{}) Logger {
	return &logrusLogger{logger: l.logger.WithField(key, value)}
}

func (l *logrusLogger) WithFields(fields map[string]interface{}) Logger {
	return &logrusLogger{logger: l.logger.WithFields(fields)}
}

func (l *logrusLogger) WithContext(ctx context.Context) Logger {
	return &logrusLogger{logger: l.logger.WithContext(ctx)}
}

func (l *logrusLogger) Warn(ctx context.Context, msg string, args ...any) {
	entry := l.logger.WithContext(ctx)
	if traceID := getTraceID(ctx); traceID != "" {
		entry = entry.WithField("trace_id", traceID)
	}
	args = append([]any{getCallerFunctionName()}, args...)
	entry.Warnf("[%s] "+msg, args...)
}

func (l *logrusLogger) Error(ctx context.Context, msg string, args ...any) {
	entry := l.logger.WithContext(ctx)
	if traceID := getTraceID(ctx); traceID != "" {
		entry = entry.WithField("trace_id", traceID)
	}
	args = append([]any{getCallerFunctionName()}, args...)
	entry.Errorf("[%s] "+msg, args...)
}

func (l *logrusLogger) Info(ctx context.Context, msg string, args ...any) {
	entry := l.logger.WithContext(ctx)
	if traceID := getTraceID(ctx); traceID != "" {
		entry = entry.WithField("trace_id", traceID)
	}
	args = append([]any{getCallerFunctionName()}, args...)
	entry.Infof("[%s] "+msg, args...)
}

func (l *logrusLogger) Debug(ctx context.Context, msg string, args ...any) {
	entry := l.logger.WithContext(ctx)
	if traceID := getTraceID(ctx); traceID != "" {
		entry = entry.WithField("trace_id", traceID)
	}
	args = append([]any{getCallerFunctionName()}, args...)
	entry.Debugf("[%s] "+msg, args...)
}

// moduleLoggers 存储各模块的 logger
var moduleLoggers = make(map[Module]Logger)

// defaultLogger 默认 logger（系统级）
var defaultLogger Logger

// LoggerConfig 日志配置
type LoggerConfig struct {
	Level      string `json:"level,omitempty" yaml:"level,omitempty" toml:"level,omitempty"`
	BaseDir    string `json:"base_dir,omitempty" yaml:"base_dir,omitempty" toml:"base_dir,omitempty"` // 日志目录
	File       string `json:"file,omitempty" yaml:"file,omitempty" toml:"file,omitempty"`             // 兼容旧配置：单文件模式
	MaxSize    int    `json:"max_size,omitempty" yaml:"max_size,omitempty" toml:"max_size,omitempty"` // 单个日志文件最大大小 (MB),默认 100MB
	MaxBackups int    `json:"max_backups,omitempty" yaml:"max_backups,omitempty" toml:"max_backups,omitempty"` // 保留的旧日志文件最大数量，默认 30 个
	MaxAge     int    `json:"max_age,omitempty" yaml:"max_age,omitempty" toml:"max_age,omitempty"`          // 保留旧日志文件的最大天数，默认 30 天
	Compress   bool   `json:"compress,omitempty" yaml:"compress,omitempty" toml:"compress,omitempty"`       // 是否压缩旧日志，默认 true
	EnableFile bool   `json:"enable_file,omitempty" yaml:"enable_file,omitempty" toml:"enable_file,omitempty"` // 是否启用文件日志，默认 true
}

// 日志文件按小时划分，便于管理和检索
// 文件名格式：2026-03-10-14.log (日期 -小时.log)

// createWriter 为指定模块创建日志写入器
func createWriter(cfg *LoggerConfig, module Module) *lumberjack.Logger {
	if !cfg.EnableFile {
		return nil
	}

	var filename string
	if cfg.BaseDir != "" {
		// 多模块模式：按模块分类到不同子目录
		moduleDir := filepath.Join(cfg.BaseDir, string(module))
		if err := os.MkdirAll(moduleDir, 0o755); err != nil {
			fmt.Printf("Warning: failed to create log directory %s: %v\n", moduleDir, err)
		}
		// 按日期 + 小时生成文件名：module/2026-03-10-14.log
		// 这样每个小时一个文件，便于检索和管理
		filename = filepath.Join(moduleDir, fmt.Sprintf("%s-%02d.log",
			time.Now().Format("2006-01-02"), time.Now().Hour()))
	} else if cfg.File != "" {
		// 单文件兼容模式
		filename = cfg.File
	} else {
		return nil
	}

	return &lumberjack.Logger{
		Filename:   filename,
		MaxSize:    cfg.getMaxSize(),
		MaxBackups: cfg.getMaxBackups(),
		MaxAge:     cfg.getMaxAge(),
		Compress:   cfg.Compress,
		LocalTime:  true, // 使用本地时间
	}
}

func (cfg *LoggerConfig) getMaxSize() int {
	if cfg.MaxSize <= 0 {
		return 100
	}
	return cfg.MaxSize
}

func (cfg *LoggerConfig) getMaxBackups() int {
	if cfg.MaxBackups <= 0 {
		return 30
	}
	return cfg.MaxBackups
}

func (cfg *LoggerConfig) getMaxAge() int {
	if cfg.MaxAge <= 0 {
		return 30
	}
	return cfg.MaxAge
}

// InitLogger 初始化日志系统
func InitLogger(cfg *LoggerConfig) {
	// 如果没有配置，使用默认配置
	if cfg == nil {
		cfg = &LoggerConfig{
			Level:      "info",
			BaseDir:    "./log",
			MaxSize:    100,
			MaxBackups: 30,
			MaxAge:     30,
			Compress:   true,
			EnableFile: true,
		}
	}

	// 确保日志目录存在
	if cfg.BaseDir != "" {
		if err := os.MkdirAll(cfg.BaseDir, 0o755); err != nil {
			fmt.Printf("Warning: failed to create log base directory %s: %v\n", cfg.BaseDir, err)
		}
	}

	// 解析日志级别
	level, err := logrus.ParseLevel(cfg.Level)
	if err != nil {
		level = logrus.InfoLevel
	}

	// 为每个模块创建独立的 logger
	modules := []Module{ModuleSystem, ModuleAPI, ModuleBrowser, ModuleScript, ModuleAgent, ModuleLLM, ModuleMCP, ModuleTask}

	for _, module := range modules {
		moduleLogger := createModuleLogger(module, level, cfg)
		moduleLoggers[module] = moduleLogger

		// 默认 logger 使用 system 模块
		if module == ModuleSystem {
			defaultLogger = moduleLogger
		}
	}
}

// createModuleLogger 为指定模块创建 logger
func createModuleLogger(module Module, level logrus.Level, cfg *LoggerConfig) Logger {
	log := logrus.New()
	log.SetLevel(level)

	// 配置 JSON 格式，方便检索和分析
	log.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: "2006-01-02 15:04:05",
		FieldMap: logrus.FieldMap{
			logrus.FieldKeyTime:  "timestamp",
			logrus.FieldKeyLevel: "level",
			logrus.FieldKeyMsg:   "message",
		},
	})

	// 添加模块字段
	baseEntry := log.WithField("module", module)

	// 设置输出目标
	writer := createWriter(cfg, module)
	if writer != nil {
		// 同时输出到文件和控制台（开发模式）
		// 如果需要只输出到文件，使用：log.SetOutput(writer)
		multiWriter := getMultiWriter(writer)
		baseEntry.Logger.SetOutput(multiWriter)
	}

	// 添加公共字段
	baseEntry = baseEntry.WithFields(logrus.Fields{
		"app":      "browserwing",
		"hostname": getHostname(),
	})

	return &logrusLogger{logger: baseEntry}
}

// getMultiWriter 返回多路写入器（文件 + 控制台）
func getMultiWriter(fileWriter *lumberjack.Logger) io.Writer {
	// 检查是否是调试模式
	debug := os.Getenv("LOG_DEBUG") == "true" || os.Getenv("DEBUG") == "true"

	if debug {
		// 调试模式：同时输出到文件和控制台
		return &multiWriter{file: fileWriter, console: os.Stdout}
	}
	// 生产模式：只输出到文件
	return fileWriter
}

// multiWriter 多路写入器
type multiWriter struct {
	file    io.Writer
	console io.Writer
}

func (m *multiWriter) Write(p []byte) (n int, err error) {
	// 写入文件
	n1, err1 := m.file.Write(p)
	// 写入控制台
	n2, err2 := m.console.Write(p)
	if err1 != nil {
		return n1, err1
	}
	return n2, err2
}

// getHostname 获取主机名
func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}

// GetLogger 获取指定模块的 logger
func GetLogger(module Module) Logger {
	if logger, ok := moduleLoggers[module]; ok {
		return logger
	}
	// 如果模块不存在，返回默认 logger
	return defaultLogger
}

// ModuleLogger 获取指定模块的 logger（便捷方法）
func ModuleLogger(module Module) Logger {
	return GetLogger(module)
}

// 便捷函数 - 使用默认 system 模块
func Warn(ctx context.Context, msg string, args ...any) {
	defaultLogger.Warn(ctx, msg, args...)
}

func Error(ctx context.Context, msg string, args ...any) {
	defaultLogger.Error(ctx, msg, args...)
}

func Info(ctx context.Context, msg string, args ...any) {
	defaultLogger.Info(ctx, msg, args...)
}

func Debug(ctx context.Context, msg string, args ...any) {
	defaultLogger.Debug(ctx, msg, args...)
}

// GetDefaultLogger 获取默认 logger
func GetDefaultLogger() Logger {
	return defaultLogger
}

// TraceID context key
type contextKey string

const traceIDKey contextKey = "trace_id"

// WithTraceID 将 trace_id 添加到 context
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

// getTraceID 从 context 中获取 trace_id
func getTraceID(ctx context.Context) string {
	if traceID, ok := ctx.Value(traceIDKey).(string); ok {
		return traceID
	}
	return ""
}

// GetTraceID 导出的获取 trace_id 函数
func GetTraceID(ctx context.Context) string {
	return getTraceID(ctx)
}
