# MCP Server Observability Implementation Plan

## Overview
Add comprehensive observability to the grep.app MCP server to track client usage patterns, especially focusing on zero-result queries and subsequent client behavior.

## Current State Analysis

**Files:**
- `main.go:942` - Single file containing entire MCP server
- Two tools: `searchCode` (line 710-857) & `batchRetrievalTool` (line 860-923)
- Basic console logging exists (`log.Printf` statements throughout)
- Cache system in place (lines 113-211)

**Problems to Solve:**
1. MCP clients struggle with effective tool usage
2. Need to track search patterns & zero-result scenarios
3. Want to understand client behavior after failed searches
4. No structured logging or analytics currently

## Implementation Plan

### Phase 1: Structured Logging Infrastructure

#### 1.1 Create `observability.go`
**Location:** `/observability.go`

**Core Types:**
```go
type LogEntry struct {
    Timestamp time.Time              `json:"timestamp"`
    Level     LogLevel               `json:"level"`
    Message   string                 `json:"message"`
    SessionID string                 `json:"session_id"`
    Tool      string                 `json:"tool"`
    Data      map[string]interface{} `json:"data"`
}

type SearchLogData struct {
    Query         string            `json:"query"`
    UseRegex      bool              `json:"use_regex"`
    Filters       map[string]string `json:"filters"`
    ResultCount   int               `json:"result_count"`
    FileCount     int               `json:"file_count"`
    Duration      time.Duration     `json:"duration_ms"`
    Success       bool              `json:"success"`
    Error         string            `json:"error,omitempty"`
    CacheHit      bool              `json:"cache_hit"`
    PagesScanned  int               `json:"pages_scanned"`
}

type BatchRetrievalLogData struct {
    Query         string   `json:"query"`
    RequestedNums []int    `json:"requested_numbers"`
    FilesSuccess  int      `json:"files_success"`
    FilesError    int      `json:"files_error"`
    Duration      time.Duration `json:"duration_ms"`
    Success       bool     `json:"success"`
}
```

**Key Features:**
- JSON Lines format for easy parsing
- Session ID correlation (UUID-based)
- Daily log rotation (`mcp-server-2024-01-15.jsonl`)
- Log levels: INFO, WARN, ERROR, DEBUG
- Structured data fields for analysis

#### 1.2 Logger Interface
```go
type ObservabilityLogger struct {
    logFile   *os.File
    sessionID string
}

// Methods:
- LogSearchStart(query, args)
- LogSearchComplete(SearchLogData)
- LogBatchRetrievalStart(query, nums)
- LogBatchRetrievalComplete(BatchRetrievalLogData)
- LogAPIRequest(url, duration, statusCode, error)
- LogCacheOperation(key, hit, query)
- LogError(tool, message, error, data)
```

### Phase 2: Integration Points

#### 2.1 searchCode Tool Integration
**File:** `main.go:725-857`

**Integration Points:**
1. **Start:** Line 730 - Log search initiation
2. **Cache Check:** Line 332-342 - Log cache hits/misses
3. **API Requests:** Line 377-409 - Log each API call
4. **Zero Results:** Line 801-804 - Special logging for empty results
5. **Completion:** Line 829 - Log final results with metrics

#### 2.2 batchRetrievalTool Integration
**File:** `main.go:867-923`

**Integration Points:**
1. **Start:** Line 869 - Log retrieval initiation
2. **GitHub API:** Line 455-516 - Log file fetch operations
3. **Completion:** Line 899 - Log success/failure metrics

#### 2.3 Session Management
- Generate session ID at server startup (main.go:692)
- Track session throughout request lifecycle
- Correlate related operations (search → batch retrieval)

### Phase 3: Target Metrics & Queries

#### 3.1 Search Pattern Analysis
**Log Data Points:**
- Search terms used by clients
- Filter combinations (language, repo, path)
- Regex vs literal searches
- Result counts (especially zeros)

**Analysis Questions:**
1. What search terms return zero results most often?
2. Which language filters are most effective?
3. Are clients using regex appropriately?
4. What's the average results-per-query?

#### 3.2 Client Behavior Tracking
**Session Correlation:**
- Sequence of queries within same session
- Time gaps between requests
- Pattern: zero results → modified query → results

**Zero-Result Recovery Patterns:**
1. Query A → 0 results
2. Query B (modified) → X results
3. Track modification patterns:
   - Added/removed filters
   - Broadened/narrowed terms
   - Regex enabled/disabled

#### 3.3 Performance Metrics
- API response times
- Cache hit rates
- Pages scanned per query
- GitHub file retrieval success rates

### Phase 4: Log Analysis System

#### 4.1 Go Log Analyzer (`analyzer/main.go`)
**Purpose:** Parse JSON logs & generate HTML dashboard

**Core Functions:**
```go
type LogAnalyzer struct {
    entries []LogEntry
    sessions map[string][]LogEntry
}

// Methods:
- LoadLogs(directory) 
- AnalyzeSearchPatterns()
- AnalyzeZeroResultQueries()  
- AnalyzeClientBehavior()
- GenerateReport() *AnalysisReport
```

**Analysis Types:**
1. **Search Term Frequency** - Most/least common queries
2. **Zero Result Analysis** - Failed queries & recovery patterns
3. **Session Flow Analysis** - Client behavior sequences
4. **Performance Stats** - API times, cache effectiveness
5. **Filter Usage** - Which filters help/hurt success rates

#### 4.2 HTML Dashboard Template
**Location:** `analyzer/templates/dashboard.html`

**Sections:**
1. **Overview Stats**
   - Total searches, sessions, zero results
   - Average results per query
   - Most active time periods

2. **Search Analysis**
   - Top search terms table
   - Zero-result queries list
   - Filter effectiveness chart

3. **Client Behavior**
   - Session flow visualization
   - Zero-result recovery patterns
   - Query modification patterns

4. **Performance Metrics**
   - API response time histogram
   - Cache hit rate over time
   - Error rate trends

### Phase 5: Implementation Steps

#### Step 1: Core Infrastructure
1. Create `observability.go` with types & logger
2. Add log directory creation in `main()`
3. Initialize global logger instance

#### Step 2: Tool Integration
1. Integrate logging into `searchCode` tool
2. Integrate logging into `batchRetrievalTool`
3. Add session correlation

#### Step 3: Log Analyzer
1. Create `analyzer/` directory structure
2. Implement log parsing & analysis
3. Create HTML template
4. Add report generation

#### Step 4: Testing & Validation
1. Test with sample queries
2. Verify log format consistency
3. Test analyzer with generated logs
4. Validate HTML output

## File Structure After Implementation

```
grep_app_mcp_golang/
├── main.go                    # Modified with logging calls
├── observability.go           # New: Structured logging system
├── go.mod                     # Updated dependencies
├── logs/                      # New: Log files directory
│   └── mcp-server-YYYY-MM-DD.jsonl
├── analyzer/                  # New: Log analysis system
│   ├── main.go               # Log analyzer CLI
│   ├── templates/
│   │   └── dashboard.html    # HTML report template
│   └── reports/              # Generated HTML reports
└── docs/work/
    └── observability-plan.md # This file
```

## Expected Log Format Examples

### Search Log Entry
```json
{
  "timestamp": "2024-01-15T10:30:45Z",
  "level": "INFO",
  "message": "Search completed: function main (results: 0)",
  "session_id": "abc12345",
  "tool": "searchCode",
  "data": {
    "search_data": {
      "query": "function main",
      "use_regex": false,
      "filters": {"lang": "go"},
      "result_count": 0,
      "duration_ms": 1250,
      "success": true,
      "cache_hit": false,
      "pages_scanned": 3
    }
  }
}
```

### Batch Retrieval Log Entry
```json
{
  "timestamp": "2024-01-15T10:31:15Z",
  "level": "INFO", 
  "message": "Batch retrieval completed: function main (2 success, 0 errors)",
  "session_id": "abc12345",
  "tool": "batchRetrievalTool",
  "data": {
    "batch_data": {
      "query": "function main",
      "requested_numbers": [1, 3],
      "files_success": 2,
      "files_error": 0,
      "duration_ms": 850,
      "success": true
    }
  }
}
```

## Success Metrics

1. **Complete observability** of all MCP tool invocations
2. **Zero-result query tracking** with client behavior analysis
3. **HTML dashboard** showing usage patterns
4. **Actionable insights** for improving client tool usage
5. **Performance monitoring** of API calls and caching

This plan provides comprehensive observability while maintaining the existing functionality and performance of the MCP server.