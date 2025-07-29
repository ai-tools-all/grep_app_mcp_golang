# MCP Log Analyzer

A Go-based tool for analyzing JSON log files from MCP (Model Context Protocol) servers to understand search patterns, performance metrics, and client behavior.

## Overview

This analyzer processes `.jsonl` log files to generate insights about:
- Search query patterns and success rates
- Zero-result queries and failure analysis  
- Client session behavior and recovery patterns
- Performance metrics (cache hit rates, API usage, response times)
- HTML dashboard reports with visualizations

## Prerequisites

- Go 1.24.3 or later
- Log files in JSONL format from your MCP server

## Installation

```bash
cd analyzer
go mod tidy
```

## Usage

### Basic Analysis

```bash
# Analyze logs from a directory
go run main.go <log-directory> [output-file]

# Example: Analyze logs and generate HTML report
go run main.go ../logs reports/dashboard.html
```

### Build and Run

```bash
# Build the analyzer
go build -o analyzer main.go

# Run the built binary
./analyzer ../logs reports/dashboard.html
```

## Input Format

The analyzer expects JSONL files containing log entries with this structure:

```json
{
  "timestamp": "2024-01-15T10:30:00Z",
  "level": "INFO",
  "message": "Search completed",
  "session_id": "sess_123",
  "tool": "searchCode",
  "data": {
    "search_data": {
      "query": "function authenticate",
      "result_count": 15,
      "duration_ms": 250,
      "success": true,
      "cache_hit": false,
      "api_requests": 2
    }
  }
}
```

## Output

### Console Output
Shows summary statistics:
```
Analysis Summary:
- Total entries: 1,234
- Total sessions: 45
- Total searches: 890
- Zero result rate: 12.3%
- Cache hit rate: 67.8%
- Average duration: 180ms
```

### HTML Dashboard
Generates a comprehensive dashboard (`reports/dashboard.html`) with:
- Overview statistics cards
- Top search queries table
- Zero-result queries analysis
- Client session behavior patterns
- Performance insights and recommendations

## Analysis Features

### Query Pattern Analysis
- Most popular search queries
- Success rates per query
- Filter usage patterns
- Average result counts

### Zero-Result Analysis
- Queries that return no results
- Failure rates and patterns
- Common unsuccessful search terms

### Session Behavior
- Client search sessions
- Query sequences and timing
- Recovery patterns (failed → successful queries)
- Session duration analysis

### Performance Metrics
- Average search duration
- API request patterns
- Cache hit/miss rates
- Error rates and reliability

## Directory Structure

```
analyzer/
├── main.go              # Main analyzer logic
├── go.mod              # Go module file
├── templates/
│   └── dashboard.html  # HTML report template
└── reports/            # Generated reports output
    └── dashboard.html  # Generated dashboard
```

## Log File Discovery

The analyzer recursively searches the log directory for all `.jsonl` files and processes them automatically.

## Error Handling

- Skips malformed JSON lines with warnings
- Continues processing if individual files fail
- Reports parsing errors with file and line numbers

## Security

This tool is designed for defensive security analysis only - it reads and analyzes existing log files without modifying them or performing any potentially harmful operations.

## Example Commands

```bash
# Analyze logs from parent directory
go run main.go ../logs

# Specify custom output location
go run main.go ../logs /tmp/analysis.html

# Analyze logs from specific date
go run main.go ../logs/2024-01-15 reports/daily-report.html
```

## Troubleshooting

1. **No log files found**: Ensure the directory contains `.jsonl` files
2. **Permission errors**: Check read permissions on log directory
3. **Template errors**: Verify `templates/dashboard.html` exists
4. **Memory issues**: For large log volumes, consider processing smaller date ranges

## Performance

- Processes thousands of log entries efficiently
- Memory usage scales with log volume
- HTML generation is lightweight using Go templates