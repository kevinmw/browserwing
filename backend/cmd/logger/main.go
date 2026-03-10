// logger - BrowserWing 日志检索工具
//
// 用法:
//   logger search [选项]     # 搜索日志
//   logger stats [选项]      # 查看统计信息
//   logger modules           # 列出所有模块
//   logger tail [选项]       # 实时查看日志
//
// 示例:
//   logger search -m script -l error --today
//   logger search -m browser --script-id xxx
//   logger stats -m api --today
//   logger tail -m agent

package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// LogEntry 日志条目结构（与 logger.go 中的 JSON 格式对应）
type LogEntry struct {
	Timestamp string                 `json:"timestamp"`
	Level     string                 `json:"level"`
	Message   string                 `json:"message"`
	Module    string                 `json:"module"`
	TraceID   string                 `json:"trace_id,omitempty"`
	ScriptID  string                 `json:"script_id,omitempty"`
	SessionID string                 `json:"session_id,omitempty"`
	Hostname  string                 `json:"hostname,omitempty"`
	App       string                 `json:"app,omitempty"`
	Extra     map[string]interface{} `json:"-"`
}

// SearchOptions 搜索选项
type SearchOptions struct {
	Module    string
	Level     string
	Keyword   string
	TraceID   string
	ScriptID  string
	SessionID string
	StartTime time.Time
	EndTime   time.Time
	Limit     int
	LogDir    string
	Date      string
	Today     bool
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "search":
		runSearch(os.Args[2:])
	case "stats":
		runStats(os.Args[2:])
	case "modules":
		runModules()
	case "tail":
		runTail(os.Args[2:])
	case "help":
		printUsage()
	default:
		fmt.Printf("未知命令：%s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`BrowserWing 日志检索工具

用法：
  logger <command> [选项]

命令:
  search      搜索日志
  stats       查看统计信息
  modules     列出所有模块
  tail        实时查看日志
  help        显示帮助

示例:
  logger search -m script -l error --today
  logger search -m browser --script-id abc123
  logger stats -m api --today
  logger tail -m agent -l error

使用 "logger <command> -h" 查看具体命令的帮助
`)
}

// runSearch 运行搜索命令
func runSearch(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	module := fs.String("m", "", "模块名称 (system/api/browser/script/agent/llm/mcp/task)")
	level := fs.String("l", "", "日志级别 (debug/info/warn/error)")
	keyword := fs.String("k", "", "关键词搜索")
	traceID := fs.String("trace", "", "Trace ID")
	scriptID := fs.String("script-id", "", "脚本 ID")
	sessionID := fs.String("session", "", "会话 ID")
	date := fs.String("date", "", "日期 (YYYY-MM-DD)")
	today := fs.Bool("today", false, "搜索今天的日志")
	limit := fs.Int("n", 100, "最大返回条数")
	logDir := fs.String("dir", "./log", "日志目录")
	output := fs.String("o", "text", "输出格式 (text/json)")

	fs.Parse(args)

	opts := SearchOptions{
		Module:    *module,
		Level:     *level,
		Keyword:   *keyword,
		TraceID:   *traceID,
		ScriptID:  *scriptID,
		SessionID: *sessionID,
		Date:      *date,
		Today:     *today,
		Limit:     *limit,
		LogDir:    *logDir,
	}

	if *today && *date == "" {
		opts.Date = time.Now().Format("2006-01-02")
	}

	entries, err := searchLogs(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "搜索失败：%v\n", err)
		os.Exit(1)
	}

	// 输出结果
	if *output == "json" {
		for _, e := range entries {
			data, _ := json.Marshal(e)
			fmt.Println(string(data))
		}
	} else {
		printEntries(entries)
	}
}

// searchLogs 搜索日志文件
func searchLogs(opts SearchOptions) ([]LogEntry, error) {
	var entries []LogEntry

	// 确定要搜索的目录
	searchDir := opts.LogDir
	if opts.Module != "" {
		searchDir = filepath.Join(searchDir, opts.Module)
	}

	// 获取要搜索的文件列表
	files, err := getLogFiles(searchDir, opts.Date)
	if err != nil {
		return nil, err
	}

	// 编译正则表达式（如果需要）
	var keywordRegex *regexp.Regexp
	if opts.Keyword != "" {
		keywordRegex, err = regexp.Compile("(?i)" + regexp.QuoteMeta(opts.Keyword))
		if err != nil {
			return nil, fmt.Errorf("无效的关键词：%v", err)
		}
	}

	// 逐个文件搜索
	for _, file := range files {
		fileEntries, err := searchFile(file, opts, keywordRegex)
		if err != nil {
			continue // 跳过读取失败的文件
		}
		entries = append(entries, fileEntries...)

		if len(entries) >= opts.Limit {
			entries = entries[:opts.Limit]
			break
		}
	}

	// 按时间排序
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp < entries[j].Timestamp
	})

	return entries, nil
}

// getLogFiles 获取要搜索的日志文件列表
func getLogFiles(dir string, date string) ([]string, error) {
	var files []string

	// 如果指定了日期，搜索该日期的所有文件（包括按小时划分的）
	if date != "" {
		// 遍历子目录搜索匹配的文件
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && strings.HasSuffix(path, ".log") {
				base := filepath.Base(path)
				base = strings.TrimSuffix(base, ".log")
				// 匹配日期格式：2026-03-10 或 2026-03-10-14
				if strings.HasPrefix(base, date) {
					files = append(files, path)
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return files, nil
	}

	// 搜索所有.log 文件（包括子目录）
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.HasSuffix(path, ".log") {
			// 检查文件修改时间，只取最近 7 天的
			if time.Since(info.ModTime()).Hours() < 24*7 {
				files = append(files, path)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return files, nil
}

// searchFile 在单个文件中搜索
func searchFile(filename string, opts SearchOptions, keywordRegex *regexp.Regexp) ([]LogEntry, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []LogEntry
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry LogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // 跳过无法解析的行
		}

		// 过滤条件
		if !matchesFilter(entry, opts, keywordRegex) {
			continue
		}

		entries = append(entries, entry)
	}

	return entries, scanner.Err()
}

// matchesFilter 检查条目是否匹配过滤条件
func matchesFilter(entry LogEntry, opts SearchOptions, keywordRegex *regexp.Regexp) bool {
	// 按级别过滤
	if opts.Level != "" && !strings.EqualFold(entry.Level, opts.Level) {
		return false
	}

	// 按 TraceID 过滤
	if opts.TraceID != "" && entry.TraceID != opts.TraceID {
		return false
	}

	// 按 ScriptID 过滤
	if opts.ScriptID != "" && entry.ScriptID != opts.ScriptID {
		return false
	}

	// 按 SessionID 过滤
	if opts.SessionID != "" && entry.SessionID != opts.SessionID {
		return false
	}

	// 按时间范围过滤
	if !opts.StartTime.IsZero() {
		entryTime, _ := time.Parse("2006-01-02 15:04:05", entry.Timestamp)
		if entryTime.Before(opts.StartTime) {
			return false
		}
	}

	if !opts.EndTime.IsZero() {
		entryTime, _ := time.Parse("2006-01-02 15:04:05", entry.Timestamp)
		if entryTime.After(opts.EndTime) {
			return false
		}
	}

	// 按关键词过滤
	if keywordRegex != nil && !keywordRegex.MatchString(entry.Message) {
		return false
	}

	return true
}

// printEntries 打印日志条目
func printEntries(entries []LogEntry) {
	if len(entries) == 0 {
		fmt.Println("未找到匹配的日志")
		return
	}

	// 打印表头
	fmt.Printf("%-20s %-8s %-10s %s\n", "时间", "级别", "模块", "消息")
	fmt.Println(strings.Repeat("-", 120))

	for _, e := range entries {
		// 格式化时间，只显示时分秒
		timestamp := e.Timestamp
		if parts := strings.Fields(e.Timestamp); len(parts) > 1 {
			timestamp = parts[1]
		}

		// 截断过长的消息
		message := e.Message
		if len(message) > 70 {
			message = message[:70] + "..."
		}

		level := strings.ToUpper(e.Level)
		fmt.Printf("%-20s %-8s %-10s %s\n", timestamp, level, e.Module, message)
	}

	fmt.Printf("\n共找到 %d 条日志\n", len(entries))
}

// runStats 运行统计命令
func runStats(args []string) {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	module := fs.String("m", "", "模块名称")
	date := fs.String("date", "", "日期 (YYYY-MM-DD)")
	today := fs.Bool("today", false, "统计今天的日志")
	logDir := fs.String("dir", "./log", "日志目录")

	fs.Parse(args)

	opts := SearchOptions{
		Module: *module,
		Date:   *date,
		Today:  *today,
		LogDir: *logDir,
	}

	if *today && *date == "" {
		opts.Date = time.Now().Format("2006-01-02")
	}

	stats, err := getStats(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "统计失败：%v\n", err)
		os.Exit(1)
	}

	printStats(stats)
}

// LogStats 日志统计
type LogStats struct {
	Total      int            `json:"total"`
	ByLevel    map[string]int `json:"by_level"`
	ByHour     map[string]int `json:"by_hour"`
	TimeRange  string         `json:"time_range"`
	Module     string         `json:"module"`
}

func getStats(opts SearchOptions) (*LogStats, error) {
	stats := &LogStats{
		ByLevel: make(map[string]int),
		ByHour:  make(map[string]int),
		Module:  opts.Module,
	}

	// 确定要搜索的目录
	searchDir := opts.LogDir
	if opts.Module != "" {
		searchDir = filepath.Join(searchDir, opts.Module)
	}

	files, err := getLogFiles(searchDir, opts.Date)
	if err != nil {
		return nil, err
	}

	var minTime, maxTime string

	for _, file := range files {
		fileStats, err := statsFile(file, stats)
		if err != nil {
			continue
		}

		stats.Total += fileStats
		if fileStats > 0 {
			if minTime == "" || file < minTime {
				minTime = file
			}
			// 获取文件修改时间作为最大时间
			if info, err := os.Stat(file); err == nil {
				maxTime = info.ModTime().Format("2006-01-02 15:04:05")
			}
		}
	}

	if minTime != "" {
		minTime = strings.TrimSuffix(filepath.Base(minTime), ".log")
	}
	stats.TimeRange = fmt.Sprintf("%s ~ %s", minTime, maxTime)

	return stats, nil
}

func statsFile(filename string, stats *LogStats) (int, error) {
	file, err := os.Open(filename)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	count := 0
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry LogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}

		count++
		stats.ByLevel[strings.ToLower(entry.Level)]++

		// 按小时统计
		if t, err := time.Parse("2006-01-02 15:04:05", entry.Timestamp); err == nil {
			hour := t.Format("15:00")
			stats.ByHour[hour]++
		}
	}

	return count, scanner.Err()
}

func printStats(stats *LogStats) {
	fmt.Println("\n=== 日志统计 ===")
	fmt.Printf("模块：%s\n", stats.Module)
	fmt.Printf("时间范围：%s\n", stats.TimeRange)
	fmt.Printf("总条数：%d\n\n", stats.Total)

	// 按级别统计
	fmt.Println("按级别统计:")
	fmt.Printf("%-10s %-10s %-10s\n", "级别", "数量", "占比")
	fmt.Println(strings.Repeat("-", 40))

	levels := []string{"debug", "info", "warn", "error"}
	for _, level := range levels {
		count := stats.ByLevel[level]
		percentage := float64(0)
		if stats.Total > 0 {
			percentage = float64(count) / float64(stats.Total) * 100
		}
		fmt.Printf("%-10s %-10d %-10.1f%%\n", strings.ToUpper(level), count, percentage)
	}

	// 按小时统计
	fmt.Println("\n按小时统计:")
	fmt.Printf("%-10s %-10s\n", "小时", "数量")
	fmt.Println(strings.Repeat("-", 30))

	hours := make([]string, 0, len(stats.ByHour))
	for hour := range stats.ByHour {
		hours = append(hours, hour)
	}
	sort.Strings(hours)

	for _, hour := range hours {
		fmt.Printf("%-10s %-10d\n", hour, stats.ByHour[hour])
	}
}

// runModules 列出所有模块
func runModules() {
	modules := []string{
		"system",
		"api",
		"browser",
		"script",
		"agent",
		"llm",
		"mcp",
		"task",
	}

	fmt.Println("BrowserWing 日志模块:")
	fmt.Println()
	fmt.Printf("%-12s %s\n", "模块", "说明")
	fmt.Println(strings.Repeat("-", 60))

	descriptions := map[string]string{
		"system":  "系统启动、关闭、配置加载",
		"api":     "HTTP API 请求处理",
		"browser": "浏览器实例管理、页面操作",
		"script":  "脚本录制、回放",
		"agent":   "Agent 任务执行",
		"llm":     "LLM API 调用",
		"mcp":     "MCP 协议通信",
		"task":    "定时任务调度",
	}

	for _, module := range modules {
		fmt.Printf("%-12s %s\n", module, descriptions[module])
	}
}

// runTail 实时查看日志
func runTail(args []string) {
	fs := flag.NewFlagSet("tail", flag.ExitOnError)
	module := fs.String("m", "", "模块名称")
	level := fs.String("l", "", "日志级别过滤")
	logDir := fs.String("dir", "./log", "日志目录")
	lines := fs.Int("n", 50, "初始显示行数")

	fs.Parse(args)

	opts := SearchOptions{
		Module: *module,
		Level:  *level,
		LogDir: *logDir,
		Limit:  *lines,
		Today:  true,
	}

	// 先显示最近的日志
	opts.Date = time.Now().Format("2006-01-02")
	entries, _ := searchLogs(opts)
	printEntries(entries)

	fmt.Println("\n--- 实时监听中 (Ctrl+C 退出) ---\n")

	// 监听文件变化
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	lastCount := make(map[string]int)
	for {
		select {
		case <-ticker.C:
			// 定期检查新日志
			newEntries, _ := searchLogs(opts)
			if len(newEntries) > len(entries) {
				for i := len(entries); i < len(newEntries); i++ {
					printEntry(newEntries[i])
				}
				entries = newEntries
			}
			_ = lastCount // 避免未使用变量错误
		}
	}
}

func printEntry(entry LogEntry) {
	level := strings.ToUpper(entry.Level)
	var colorLevel string

	switch level {
	case "ERROR":
		colorLevel = "\033[31m" + level + "\033[0m"
	case "WARN":
		colorLevel = "\033[33m" + level + "\033[0m"
	case "INFO":
		colorLevel = "\033[32m" + level + "\033[0m"
	case "DEBUG":
		colorLevel = "\033[36m" + level + "\033[0m"
	default:
		colorLevel = level
	}

	timestamp := entry.Timestamp
	if parts := strings.Fields(entry.Timestamp); len(parts) > 1 {
		timestamp = parts[1]
	}

	fmt.Printf("[%s] %s [%s] %s\n", timestamp, colorLevel, entry.Module, entry.Message)
}
