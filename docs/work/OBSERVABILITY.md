# MCP Server Observability Guide

## Overview

The MCP server now includes comprehensive observability features to track client usage patterns, analyze zero-result queries, and understand client behavior patterns.

## Features Implemented

### ✅ Structured Logging
- **JSON Lines format** for easy parsing
- **Session correlation** with unique session IDs  
- **Daily log rotation** (`logs/mcp-server-YYYY-MM-DD.jsonl`)
- **Multi-level logging** (INFO, WARN, ERROR, DEBUG)

### ✅ Search Analytics
- Track all search queries and parameters
- Monitor zero-result queries specifically 
- Record filter usage patterns
- Measure API performance and cache effectiveness

### ✅ Client Behavior Tracking
- Session-based correlation of queries
- Recovery pattern analysis (zero results → modified query → success)
- Time-based behavior analysis

### ✅ HTML Dashboard
- Interactive web dashboard showing usage analytics
- Query frequency analysis
- Zero-result pattern identification
- Client recovery behavior visualization

## Usage

### 1. Run the MCP Server
```bash
# Build the server
go build -o grep_app_mcp_server main.go observability.go

# Run with stdio transport (default)
./grep_app_mcp_server

# Run with HTTP transport
./grep_app_mcp_server -transport http -port 8603
```

The server will automatically create a `logs/` directory and start logging all operations.

### 2. Analyze Logs
```bash
# Build the analyzer
cd analyzer
go build -o log_analyzer main.go

# Generate HTML report
./log_analyzer ../logs reports/dashboard.html

# View the dashboard
open reports/dashboard.html
```

## Log Format

### Search Log Entry Example
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
      "file_count": 0,
      "line_count": 0,
      "duration_ms": 1250,
      "success": true,
      "cache_hit": false,
      "pages_scanned": 3,
      "api_requests": 2
    }
  }
}
```

### Batch Retrieval Log Entry Example
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
      "files_found": 2,
      "files_success": 2,
      "files_error": 0,
      "duration_ms": 850,
      "success": true
    }
  }
}
```

## Target Analytics Queries

The system is designed to answer these key questions:

### 1. What search terms are clients using?
- **Query frequency analysis**: Most/least common search terms
- **Filter usage patterns**: Which language/repo/path filters are effective
- **Regex vs literal usage**: How often clients use regex searches

### 2. Which queries return zero results?
- **Zero-result query identification**: Specific queries that consistently fail
- **Failure rate analysis**: Success rates by query pattern
- **Filter effectiveness**: Do certain filters help or hurt success rates?

### 3. How do clients recover from zero results?
- **Recovery patterns**: What do clients try after getting zero results?
- **Sequence analysis**: Failed query → modified query → success/failure
- **Time analysis**: How quickly do clients retry with modified queries?
- **Success factors**: What modifications lead to successful recoveries?

## Dashboard Sections

### 📊 Overview Stats
- Total searches, sessions, zero result rate
- Cache hit rate, average search time
- API call efficiency metrics

### 🔍 Popular Queries
- Most frequently searched terms
- Success rates by query
- Filter usage patterns
- Average results per query

### ❌ Zero Result Analysis  
- Queries that consistently return no results
- Failure rates and patterns
- Common characteristics of failed queries

### 👤 Client Behavior
- Session-level analysis
- Recovery pattern identification
- Query modification strategies
- Time-based behavior patterns

### ⚡ Performance Insights
- Cache performance metrics
- API response times
- Error rate analysis
- Optimization recommendations

## File Structure

```
grep_app_mcp_golang/
├── main.go                    # MCP server with integrated logging
├── observability.go           # Structured logging system  
├── logs/                      # Generated log files
│   └── mcp-server-YYYY-MM-DD.jsonl
├── analyzer/                  # Log analysis system
│   ├── main.go               # Log analyzer CLI
│   ├── go.mod                # Analyzer dependencies
│   ├── templates/
│   │   └── dashboard.html    # HTML report template
│   └── reports/              # Generated HTML reports
└── docs/work/
    ├── observability-plan.md # Implementation plan
    └── OBSERVABILITY.md      # This usage guide
```

## Implementation Benefits

1. **Data-Driven Improvements**: Understand how clients actually use the MCP tools
2. **Zero-Result Optimization**: Identify and fix common query patterns that fail
3. **Client UX Insights**: See how clients recover from failed searches
4. **Performance Monitoring**: Track API efficiency and caching effectiveness
5. **Usage Analytics**: Measure tool adoption and effectiveness

## Future Enhancements

- Real-time dashboard updates
- Query suggestion algorithms based on successful patterns
- Automated failure pattern detection
- Client behavior prediction models
- Performance alerting and monitoring