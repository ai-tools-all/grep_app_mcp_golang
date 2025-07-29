# MCP Log Analyzer

Generate HTML dashboards from MCP server log files. Analyze search patterns, performance metrics, and user behavior.

## Quick Start

```bash
# Analyze single log file
go run main.go ../logs/mcp-server-2025-07-29.jsonl

# Analyze all .jsonl files in directory  
go run main.go ../logs
```

Reports are generated in `reports/` folder with interactive HTML dashboards.

## Features

- **Search Analysis**: Query patterns, success rates, zero-result tracking
- **Performance Metrics**: Cache hit rates, API usage, response times  
- **User Behavior**: Session analysis, recovery patterns, query sequences
- **Modern Dashboard**: Responsive HTML reports with visualizations

## Requirements

- Go 1.24.3+
- `.jsonl` log files from MCP servers

## Example Output

```
Analysis Summary for mcp-server-2025-07-29.jsonl:
- Total entries: 52
- Total sessions: 1  
- Total searches: 10
- Zero result rate: 20.0%
- Cache hit rate: 10.0%
✅ Report saved to: reports/mcp-server-2025-07-29.html
```

## Dashboard Features

The generated HTML dashboard includes:
- **Overview Cards**: Key metrics and statistics
- **Search Analysis**: Top queries with success rate bars
- **Zero Results**: Failed queries and patterns
- **Sessions**: User behavior and recovery patterns
- **Performance**: Cache rates, durations, error rates

Reports use responsive design with modern CSS and clear data visualization.