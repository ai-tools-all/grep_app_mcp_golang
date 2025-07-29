package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

//================================================================================
// Observability Types
//================================================================================

// LogLevel represents the severity of a log entry
type LogLevel string

const (
	LogLevelInfo  LogLevel = "INFO"
	LogLevelWarn  LogLevel = "WARN"
	LogLevelError LogLevel = "ERROR"
	LogLevelDebug LogLevel = "DEBUG"
)

const (
	logDir = "./logs"
)

// LogEntry represents a structured log entry
type LogEntry struct {
	Timestamp time.Time              `json:"timestamp"`
	Level     LogLevel               `json:"level"`
	Message   string                 `json:"message"`
	SessionID string                 `json:"session_id"`
	Tool      string                 `json:"tool"`
	Data      map[string]interface{} `json:"data"`
}

// SearchLogData contains specific data for search operations
type SearchLogData struct {
	Query         string            `json:"query"`
	UseRegex      bool              `json:"use_regex"`
	CaseSensitive bool              `json:"case_sensitive"`
	WholeWords    bool              `json:"whole_words"`
	RepoFilter    string            `json:"repo_filter,omitempty"`
	PathFilter    string            `json:"path_filter,omitempty"`
	LangFilter    string            `json:"lang_filter,omitempty"`
	ResultCount   int               `json:"result_count"`
	FileCount     int               `json:"file_count"`
	LineCount     int               `json:"line_count"`
	Duration      time.Duration     `json:"duration_ms"`
	Success       bool              `json:"success"`
	Error         string            `json:"error,omitempty"`
	CacheHit      bool              `json:"cache_hit"`
	PagesScanned  int               `json:"pages_scanned"`
	APIRequests   int               `json:"api_requests"`
	RegexFiltered bool              `json:"regex_filtered"`
	Filters       map[string]string `json:"filters"`
}

// BatchRetrievalLogData contains specific data for batch retrieval operations
type BatchRetrievalLogData struct {
	Query         string        `json:"query"`
	RequestedNums []int         `json:"requested_numbers"`
	FilesFound    int           `json:"files_found"`
	FilesSuccess  int           `json:"files_success"`
	FilesError    int           `json:"files_error"`
	Duration      time.Duration `json:"duration_ms"`
	Success       bool          `json:"success"`
	Error         string        `json:"error,omitempty"`
}

// ClientSessionData tracks client behavior patterns
type ClientSessionData struct {
	SessionID      string    `json:"session_id"`
	FirstSeen      time.Time `json:"first_seen"`
	LastSeen       time.Time `json:"last_seen"`
	TotalRequests  int       `json:"total_requests"`
	SearchQueries  []string  `json:"search_queries"`
	ZeroResults    int       `json:"zero_results"`
	SuccessResults int       `json:"success_results"`
}

//================================================================================
// Logger Interface
//================================================================================

type ObservabilityLogger struct {
	logFile   *os.File
	logDir    string
	sessionID string
}

// NewObservabilityLogger creates a new logger instance
func NewObservabilityLogger(logDir string) (*ObservabilityLogger, error) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Create log file with timestamp
	timestamp := time.Now().Format("2006-01-02")
	logPath := filepath.Join(logDir, fmt.Sprintf("mcp-server-%s.jsonl", timestamp))
	
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	sessionID := uuid.New().String()[:8] // Short session ID

	return &ObservabilityLogger{
		logFile:   logFile,
		logDir:    logDir,
		sessionID: sessionID,
	}, nil
}

// Close closes the log file
func (ol *ObservabilityLogger) Close() error {
	if ol.logFile != nil {
		return ol.logFile.Close()
	}
	return nil
}

// writeLogEntry writes a structured log entry to the file and console
func (ol *ObservabilityLogger) writeLogEntry(entry LogEntry) error {
	entry.SessionID = ol.sessionID
	entry.Timestamp = time.Now()
	
	// Write structured JSON to file
	logLine, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal log entry: %w", err)
	}
	
	_, err = ol.logFile.WriteString(string(logLine) + "\n")
	if err != nil {
		return fmt.Errorf("failed to write log entry: %w", err)
	}
	
	// Ensure immediate write to file
	if err := ol.logFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync log file: %w", err)
	}
	
	// Also write human-readable format to console
	ol.writeToConsole(entry)
	
	return nil
}

// writeToConsole writes a human-readable log entry to console
func (ol *ObservabilityLogger) writeToConsole(entry LogEntry) {
	timestamp := entry.Timestamp.Format("2006-01-02 15:04:05")
	prefix := fmt.Sprintf("[%s] %s [%s]", timestamp, entry.Level, entry.SessionID[:8])
	
	// Format console output based on log level
	switch entry.Level {
	case LogLevelError:
		log.Printf("❌ %s %s", prefix, entry.Message)
	case LogLevelWarn:
		log.Printf("⚠️  %s %s", prefix, entry.Message)
	case LogLevelInfo:
		log.Printf("ℹ️  %s %s", prefix, entry.Message)
	case LogLevelDebug:
		log.Printf("🔍 %s %s", prefix, entry.Message)
	default:
		log.Printf("%s %s", prefix, entry.Message)
	}
}

//================================================================================
// Logging Methods
//================================================================================

// LogSearchStart logs the beginning of a search operation
func (ol *ObservabilityLogger) LogSearchStart(query string, args map[string]interface{}) {
	data := map[string]interface{}{
		"query":          query,
		"arguments":      args,
		"operation":      "search_start",
	}
	
	entry := LogEntry{
		Level:   LogLevelInfo,
		Message: fmt.Sprintf("Starting search for query: %s", query),
		Tool:    "searchCode",
		Data:    data,
	}
	
	ol.writeLogEntry(entry)
}

// LogSearchComplete logs the completion of a search operation
func (ol *ObservabilityLogger) LogSearchComplete(logData SearchLogData) {
	data := map[string]interface{}{
		"search_data": logData,
		"operation":   "search_complete",
	}
	
	level := LogLevelInfo
	message := fmt.Sprintf("Search completed: %s (results: %d)", logData.Query, logData.ResultCount)
	
	if !logData.Success {
		level = LogLevelError
		message = fmt.Sprintf("Search failed: %s - %s", logData.Query, logData.Error)
	} else if logData.ResultCount == 0 {
		level = LogLevelWarn
		message = fmt.Sprintf("Search returned zero results: %s", logData.Query)
	}
	
	entry := LogEntry{
		Level:   level,
		Message: message,
		Tool:    "searchCode",
		Data:    data,
	}
	
	ol.writeLogEntry(entry)
}

// LogBatchRetrievalStart logs the beginning of a batch retrieval operation
func (ol *ObservabilityLogger) LogBatchRetrievalStart(query string, resultNumbers []int) {
	data := map[string]interface{}{
		"query":          query,
		"result_numbers": resultNumbers,
		"operation":      "batch_retrieval_start",
	}
	
	entry := LogEntry{
		Level:   LogLevelInfo,
		Message: fmt.Sprintf("Starting batch retrieval for query: %s (%d files)", query, len(resultNumbers)),
		Tool:    "batchRetrievalTool",
		Data:    data,
	}
	
	ol.writeLogEntry(entry)
}

// LogBatchRetrievalComplete logs the completion of a batch retrieval operation
func (ol *ObservabilityLogger) LogBatchRetrievalComplete(logData BatchRetrievalLogData) {
	data := map[string]interface{}{
		"batch_data": logData,
		"operation":  "batch_retrieval_complete",
	}
	
	level := LogLevelInfo
	message := fmt.Sprintf("Batch retrieval completed: %s (%d success, %d errors)", 
		logData.Query, logData.FilesSuccess, logData.FilesError)
	
	if !logData.Success {
		level = LogLevelError
		message = fmt.Sprintf("Batch retrieval failed: %s - %s", logData.Query, logData.Error)
	}
	
	entry := LogEntry{
		Level:   level,
		Message: message,
		Tool:    "batchRetrievalTool",
		Data:    data,
	}
	
	ol.writeLogEntry(entry)
}

// LogAPIRequest logs individual API requests
func (ol *ObservabilityLogger) LogAPIRequest(url string, duration time.Duration, statusCode int, err error) {
	data := map[string]interface{}{
		"url":          url,
		"duration_ms":  duration.Milliseconds(),
		"status_code":  statusCode,
		"success":      err == nil,
		"operation":    "api_request",
	}
	
	if err != nil {
		data["error"] = err.Error()
	}
	
	level := LogLevelInfo
	message := fmt.Sprintf("API request to %s (%d) in %v", url, statusCode, duration)
	
	if err != nil {
		level = LogLevelError
		message = fmt.Sprintf("API request failed: %s - %v", url, err)
	}
	
	entry := LogEntry{
		Level:   level,
		Message: message,
		Tool:    "api",
		Data:    data,
	}
	
	ol.writeLogEntry(entry)
}

// LogCacheOperation logs cache hits/misses
func (ol *ObservabilityLogger) LogCacheOperation(cacheKey string, hit bool, query string) {
	data := map[string]interface{}{
		"cache_key": cacheKey,
		"hit":       hit,
		"query":     query,
		"operation": "cache_operation",
	}
	
	message := fmt.Sprintf("Cache %s for query: %s", map[bool]string{true: "HIT", false: "MISS"}[hit], query)
	
	entry := LogEntry{
		Level:   LogLevelDebug,
		Message: message,
		Tool:    "cache",
		Data:    data,
	}
	
	ol.writeLogEntry(entry)
}

// LogError logs general errors
func (ol *ObservabilityLogger) LogError(tool string, message string, err error, data map[string]interface{}) {
	if data == nil {
		data = make(map[string]interface{})
	}
	data["error"] = err.Error()
	data["operation"] = "error"
	
	entry := LogEntry{
		Level:   LogLevelError,
		Message: fmt.Sprintf("%s: %v", message, err),
		Tool:    tool,
		Data:    data,
	}
	
	ol.writeLogEntry(entry)
}

//================================================================================
// General Logging Methods for Console + File
//================================================================================

// LogInfo logs an info message to both console and file
func (ol *ObservabilityLogger) LogInfo(message string, tool string, data map[string]interface{}) {
	if data == nil {
		data = make(map[string]interface{})
	}
	
	entry := LogEntry{
		Level:   LogLevelInfo,
		Message: message,
		Tool:    tool,
		Data:    data,
	}
	
	ol.writeLogEntry(entry)
}

// LogWarn logs a warning message to both console and file
func (ol *ObservabilityLogger) LogWarn(message string, tool string, data map[string]interface{}) {
	if data == nil {
		data = make(map[string]interface{})
	}
	
	entry := LogEntry{
		Level:   LogLevelWarn,
		Message: message,
		Tool:    tool,
		Data:    data,
	}
	
	ol.writeLogEntry(entry)
}

// LogErrorMsg logs an error message to both console and file
func (ol *ObservabilityLogger) LogErrorMsg(message string, tool string, err error, data map[string]interface{}) {
	if data == nil {
		data = make(map[string]interface{})
	}
	if err != nil {
		data["error"] = err.Error()
	}
	
	entry := LogEntry{
		Level:   LogLevelError,
		Message: message,
		Tool:    tool,
		Data:    data,
	}
	
	ol.writeLogEntry(entry)
}

// LogDebug logs a debug message to both console and file
func (ol *ObservabilityLogger) LogDebug(message string, tool string, data map[string]interface{}) {
	if data == nil {
		data = make(map[string]interface{})
	}
	
	entry := LogEntry{
		Level:   LogLevelDebug,
		Message: message,
		Tool:    tool,
		Data:    data,
	}
	
	ol.writeLogEntry(entry)
}

//================================================================================
// Global Logger Instance
//================================================================================

var globalLogger *ObservabilityLogger

// InitGlobalLogger initializes the global logger instance
func InitGlobalLogger(logDir string) error {
	var err error
	globalLogger, err = NewObservabilityLogger(logDir)
	return err
}

// CloseGlobalLogger closes the global logger
func CloseGlobalLogger() error {
	if globalLogger != nil {
		return globalLogger.Close()
	}
	return nil
}

// GetLogger returns the global logger instance
func GetLogger() *ObservabilityLogger {
	return globalLogger
}