# BrowserWing 日志系统使用指南

## 概述

BrowserWing 使用了模块化日志系统，支持：
- **多模块分离** - 每个模块的日志独立存储到不同目录
- **按小时轮转** - 每小时生成新日志文件，便于检索和管理
- **自动压缩** - 旧日志自动压缩节省空间
- **便捷检索** - 提供 CLI 工具快速搜索日志

## 日志目录结构

```
log/
├── system/          # 系统日志
│   ├── 2026-03-10-10.log   # 10 点的日志
│   ├── 2026-03-10-11.log   # 11 点的日志
│   └── 2026-03-10-14.log   # 14 点的日志
├── api/             # API 请求日志
│   ├── 2026-03-10-10.log
│   └── 2026-03-10-11.log
├── browser/         # 浏览器操作日志
│   └── 2026-03-10-10.log
├── script/          # 脚本执行日志
│   └── 2026-03-10-10.log
├── agent/           # Agent 任务日志
│   └── 2026-03-10-10.log
├── llm/             # LLM 调用日志
│   └── 2026-03-10-10.log
├── mcp/             # MCP 协议日志
│   └── 2026-03-10-10.log
└── task/            # 定时任务日志
    └── 2026-03-10-10.log
```

**文件名格式**：`YYYY-MM-DD-HH.log`（按小时划分）

## 配置说明

在 `config.toml` 中配置日志：

```toml
[log]
level = 'info'              # 日志级别：debug/info/warn/error
base_dir = './log'          # 日志基础目录
enable_file = true          # 是否启用文件日志
max_size = 100              # 单个文件最大大小 (MB)
max_backups = 30            # 最多保留的备份文件数
max_age = 30                # 日志文件最大保留天数
compress = true             # 是否压缩旧日志
```

## 日志检索工具

使用 `logger.exe` 命令检索日志：

### 1. 搜索日志

```bash
# 搜索今天的 error 日志
logger.exe search -l error --today

# 搜索 script 模块的日志
logger.exe search -m script

# 搜索特定脚本的执行日志
logger.exe search -m script --script-id abc123

# 关键词搜索
logger.exe search -k "timeout" -m llm

# 搜索特定 TraceID 的完整调用链
logger.exe search --trace xxx-xxx-xxx

# 导出为 JSON 格式
logger.exe search -m api -o json
```

### 2. 查看统计信息

```bash
# 查看今天的日志统计
logger.exe stats --today

# 查看特定模块的统计
logger.exe stats -m api --today

# 查看指定日期的统计
logger.exe stats -m script -date 2026-03-10
```

### 3. 列出所有模块

```bash
logger.exe modules
```

### 4. 实时查看日志

```bash
# 实时查看 agent 模块日志
logger.exe tail -m agent

# 只看 error 级别
logger.exe tail -m llm -l error
```

## 命令行选项

### search 命令

| 选项 | 说明 | 示例 |
|------|------|------|
| `-m` | 模块名称 | `-m script` |
| `-l` | 日志级别 | `-l error` |
| `-k` | 关键词 | `-k "timeout"` |
| `--trace` | Trace ID | `--trace abc-123` |
| `--script-id` | 脚本 ID | `--script-id xxx` |
| `--session` | 会话 ID | `--session yyy` |
| `-n` | 最大返回条数 | `-n 50` |
| `--today` | 搜索今天 | `--today` |
| `-date` | 指定日期 | `-date 2026-03-10` |
| `-o` | 输出格式 | `-o json` |
| `-dir` | 日志目录 | `-dir ./log` |

### stats 命令

| 选项 | 说明 | 示例 |
|------|------|------|
| `-m` | 模块名称 | `-m api` |
| `--today` | 统计今天 | `--today` |
| `-date` | 指定日期 | `-date 2026-03-10` |
| `-dir` | 日志目录 | `-dir ./log` |

### tail 命令

| 选项 | 说明 | 示例 |
|------|------|------|
| `-m` | 模块名称 | `-m agent` |
| `-l` | 日志级别 | `-l error` |
| `-n` | 初始行数 | `-n 100` |
| `-dir` | 日志目录 | `-dir ./log` |

## 在代码中使用

### 获取模块 Logger

```go
import "github.com/browserwing/browserwing/pkg/logger"

// 获取特定模块的 logger
apiLogger := logger.GetLogger(logger.ModuleAPI)
scriptLogger := logger.GetLogger(logger.ModuleScript)

// 记录日志
apiLogger.Info(ctx, "API request received", "path", r.URL.Path)
scriptLogger.Error(ctx, "Script execution failed", "error", err, "script_id", scriptID)
```

### 添加上下文信息

```go
// 添加 TraceID
ctx = logger.WithTraceID(ctx, "abc-123-xyz")

// 链式调用
logger.GetLogger(logger.ModuleScript).
    WithField("script_id", scriptID).
    WithField("session_id", sessionID).
    Info(ctx, "Script started")
```

### 日志级别

```go
logger.Debug(ctx, "调试信息", "key", "value")
logger.Info(ctx, "普通信息", "key", "value")
logger.Warn(ctx, "警告信息", "key", "value")
logger.Error(ctx, "错误信息", "key", "value")
```

## 日志格式

日志采用 JSON 格式，便于检索和分析：

```json
{
  "timestamp": "2026-03-10 14:30:00",
  "level": "info",
  "message": "Script execution completed",
  "module": "script",
  "trace_id": "abc-123-xyz",
  "script_id": "script-001",
  "session_id": "session-002",
  "hostname": "DESKTOP-ABC123",
  "app": "browserwing"
}
```

## 故障排查

### 日志文件未生成

1. 检查 `enable_file` 是否为 `true`
2. 检查日志目录是否有写权限
3. 查看控制台输出（调试模式）

### 找不到特定日志

1. 确认模块名称正确
2. 检查日期范围（默认只保留 7 天）
3. 使用 `--today` 或 `-date` 指定正确日期

### 日志文件过大

调整配置：
```toml
max_size = 50        # 减小单个文件大小
max_backups = 15     # 减少备份数量
compress = true      # 启用压缩
```

## 最佳实践

1. **生产环境** - 设置 `level = 'warn'` 减少日志量
2. **开发环境** - 设置 `level = 'debug'` 并启用 `DEBUG=true`
3. **问题排查** - 使用 `--trace` 追踪完整调用链
4. **性能分析** - 使用 `stats` 命令查看日志分布
5. **定期清理** - 设置合理的 `max_age` 自动清理旧日志
