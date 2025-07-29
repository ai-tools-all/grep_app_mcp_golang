package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//================================================================================
// Types (copied from main observability.go)
//================================================================================

type LogLevel string

const (
	LogLevelInfo  LogLevel = "INFO"
	LogLevelWarn  LogLevel = "WARN"
	LogLevelError LogLevel = "ERROR"
	LogLevelDebug LogLevel = "DEBUG"
)

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

//================================================================================
// Analysis Types
//================================================================================

type QueryStats struct {
	Query       string
	Count       int
	ZeroResults int
	SuccessRate float64
	AvgResults  float64
	Filters     map[string]int
}

type SessionAnalysis struct {
	SessionID     string
	Duration      time.Duration
	Queries       []string
	ZeroResults   []string
	Recoveries    []QueryRecovery
	TotalQueries  int
	SuccessQueries int
}

type QueryRecovery struct {
	FailedQuery   string
	RecoveryQuery string
	TimeBetween   time.Duration
	Successful    bool
}

type AnalysisReport struct {
	GeneratedAt    time.Time
	TotalEntries   int
	TotalSessions  int
	TotalSearches  int
	ZeroResultRate float64
	
	// Top queries
	TopQueries        []QueryStats
	ZeroResultQueries []QueryStats
	
	// Session analysis
	Sessions []SessionAnalysis
	
	// Performance metrics
	AvgDuration      time.Duration
	AvgAPIRequests   float64
	CacheHitRate     float64
	ErrorRate        float64
	
	// Filter analysis
	FilterEffectiveness map[string]float64
}

//================================================================================
// Log Analyzer
//================================================================================

type LogAnalyzer struct {
	entries  []LogEntry
	sessions map[string][]LogEntry
}

func NewLogAnalyzer() *LogAnalyzer {
	return &LogAnalyzer{
		entries:  make([]LogEntry, 0),
		sessions: make(map[string][]LogEntry),
	}
}

func (la *LogAnalyzer) LoadLogs(logDir string) error {
	log.Printf("Loading logs from directory: %s", logDir)
	
	err := filepath.WalkDir(logDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		
		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		
		log.Printf("Processing log file: %s", path)
		return la.loadLogFile(path)
	})
	
	if err != nil {
		return fmt.Errorf("failed to walk log directory: %w", err)
	}
	
	log.Printf("Loaded %d log entries from %d sessions", len(la.entries), len(la.sessions))
	return nil
}

func (la *LogAnalyzer) loadLogFile(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer file.Close()
	
	scanner := bufio.NewScanner(file)
	lineNum := 0
	
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		
		var entry LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			log.Printf("Failed to parse line %d in %s: %v", lineNum, filePath, err)
			continue
		}
		
		la.entries = append(la.entries, entry)
		
		if la.sessions[entry.SessionID] == nil {
			la.sessions[entry.SessionID] = make([]LogEntry, 0)
		}
		la.sessions[entry.SessionID] = append(la.sessions[entry.SessionID], entry)
	}
	
	return scanner.Err()
}

func (la *LogAnalyzer) AnalyzeSearchPatterns() []QueryStats {
	queryMap := make(map[string]*QueryStats)
	
	for _, entry := range la.entries {
		if entry.Tool != "searchCode" {
			continue
		}
		
		if data, ok := entry.Data["search_data"].(map[string]interface{}); ok {
			query, _ := data["query"].(string)
			if query == "" {
				continue
			}
			
			if queryMap[query] == nil {
				queryMap[query] = &QueryStats{
					Query:   query,
					Count:   0,
					Filters: make(map[string]int),
				}
			}
			
			stat := queryMap[query]
			stat.Count++
			
			if resultCount, ok := data["result_count"].(float64); ok {
				if resultCount == 0 {
					stat.ZeroResults++
				}
				stat.AvgResults = (stat.AvgResults*float64(stat.Count-1) + resultCount) / float64(stat.Count)
			}
			
			// Track filters used
			if filters, ok := data["filters"].(map[string]interface{}); ok {
				for filterType := range filters {
					stat.Filters[filterType]++
				}
			}
			
			stat.SuccessRate = float64(stat.Count-stat.ZeroResults) / float64(stat.Count) * 100
		}
	}
	
	// Convert to slice and sort
	queries := make([]QueryStats, 0, len(queryMap))
	for _, stat := range queryMap {
		queries = append(queries, *stat)
	}
	
	sort.Slice(queries, func(i, j int) bool {
		return queries[i].Count > queries[j].Count
	})
	
	return queries
}

func (la *LogAnalyzer) AnalyzeZeroResultQueries() []QueryStats {
	allQueries := la.AnalyzeSearchPatterns()
	
	var zeroResultQueries []QueryStats
	for _, query := range allQueries {
		if query.ZeroResults > 0 {
			zeroResultQueries = append(zeroResultQueries, query)
		}
	}
	
	// Sort by zero result count
	sort.Slice(zeroResultQueries, func(i, j int) bool {
		return zeroResultQueries[i].ZeroResults > zeroResultQueries[j].ZeroResults
	})
	
	return zeroResultQueries
}

func (la *LogAnalyzer) AnalyzeClientBehavior() []SessionAnalysis {
	var sessions []SessionAnalysis
	
	for sessionID, entries := range la.sessions {
		analysis := SessionAnalysis{
			SessionID: sessionID,
			Queries:   make([]string, 0),
		}
		
		// Sort entries by timestamp
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Timestamp.Before(entries[j].Timestamp)
		})
		
		var firstTime, lastTime time.Time
		searchEntries := make([]LogEntry, 0)
		
		for _, entry := range entries {
			if firstTime.IsZero() {
				firstTime = entry.Timestamp
			}
			lastTime = entry.Timestamp
			
			if entry.Tool == "searchCode" {
				if data, ok := entry.Data["search_data"].(map[string]interface{}); ok {
					query, _ := data["query"].(string)
					if query != "" {
						analysis.Queries = append(analysis.Queries, query)
						analysis.TotalQueries++
						
						if resultCount, ok := data["result_count"].(float64); ok {
							if resultCount == 0 {
								analysis.ZeroResults = append(analysis.ZeroResults, query)
							} else {
								analysis.SuccessQueries++
							}
						}
						
						searchEntries = append(searchEntries, entry)
					}
				}
			}
		}
		
		analysis.Duration = lastTime.Sub(firstTime)
		
		// Analyze recovery patterns
		analysis.Recoveries = la.findRecoveryPatterns(searchEntries)
		
		if len(analysis.Queries) > 0 {
			sessions = append(sessions, analysis)
		}
	}
	
	// Sort by number of queries
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].TotalQueries > sessions[j].TotalQueries
	})
	
	return sessions
}

func (la *LogAnalyzer) findRecoveryPatterns(searchEntries []LogEntry) []QueryRecovery {
	var recoveries []QueryRecovery
	
	for i := 0; i < len(searchEntries)-1; i++ {
		currentEntry := searchEntries[i]
		nextEntry := searchEntries[i+1]
		
		// Check if current query had zero results
		if data, ok := currentEntry.Data["search_data"].(map[string]interface{}); ok {
			if resultCount, ok := data["result_count"].(float64); ok && resultCount == 0 {
				currentQuery, _ := data["query"].(string)
				
				// Check next query
				if nextData, ok := nextEntry.Data["search_data"].(map[string]interface{}); ok {
					nextQuery, _ := nextData["query"].(string)
					nextResultCount, _ := nextData["result_count"].(float64)
					
					if currentQuery != nextQuery {
						recovery := QueryRecovery{
							FailedQuery:   currentQuery,
							RecoveryQuery: nextQuery,
							TimeBetween:   nextEntry.Timestamp.Sub(currentEntry.Timestamp),
							Successful:    nextResultCount > 0,
						}
						recoveries = append(recoveries, recovery)
					}
				}
			}
		}
	}
	
	return recoveries
}

func (la *LogAnalyzer) GenerateReport() *AnalysisReport {
	report := &AnalysisReport{
		GeneratedAt:         time.Now(),
		TotalEntries:        len(la.entries),
		TotalSessions:       len(la.sessions),
		FilterEffectiveness: make(map[string]float64),
	}
	
	// Analyze search patterns
	allQueries := la.AnalyzeSearchPatterns()
	report.TopQueries = allQueries
	if len(allQueries) > 10 {
		report.TopQueries = allQueries[:10]
	}
	
	report.ZeroResultQueries = la.AnalyzeZeroResultQueries()
	if len(report.ZeroResultQueries) > 10 {
		report.ZeroResultQueries = report.ZeroResultQueries[:10]
	}
	
	// Analyze client behavior
	report.Sessions = la.AnalyzeClientBehavior()
	if len(report.Sessions) > 20 {
		report.Sessions = report.Sessions[:20]
	}
	
	// Calculate statistics
	var totalSearches, zeroResults int
	var totalDuration time.Duration
	var totalAPIRequests, cacheHits, totalCalls, errors int
	
	for _, entry := range la.entries {
		if entry.Tool == "searchCode" {
			if data, ok := entry.Data["search_data"].(map[string]interface{}); ok {
				totalSearches++
				
				if resultCount, ok := data["result_count"].(float64); ok && resultCount == 0 {
					zeroResults++
				}
				
				if duration, ok := data["duration_ms"].(float64); ok {
					totalDuration += time.Duration(duration) * time.Millisecond
				}
				
				if apiReqs, ok := data["api_requests"].(float64); ok {
					totalAPIRequests += int(apiReqs)
				}
				
				if !data["success"].(bool) {
					errors++
				}
			}
		}
		
		if entry.Tool == "cache" {
			totalCalls++
			if data, ok := entry.Data["hit"].(bool); ok && data {
				cacheHits++
			}
		}
	}
	
	report.TotalSearches = totalSearches
	if totalSearches > 0 {
		report.ZeroResultRate = float64(zeroResults) / float64(totalSearches) * 100
		report.AvgDuration = totalDuration / time.Duration(totalSearches)
		report.AvgAPIRequests = float64(totalAPIRequests) / float64(totalSearches)
		report.ErrorRate = float64(errors) / float64(totalSearches) * 100
	}
	
	if totalCalls > 0 {
		report.CacheHitRate = float64(cacheHits) / float64(totalCalls) * 100
	}
	
	return report
}

//================================================================================
// HTML Report Generation
//================================================================================

func (la *LogAnalyzer) GenerateHTMLReport(report *AnalysisReport, outputPath string) error {
	tmplPath := filepath.Join(filepath.Dir(outputPath), "..", "templates", "dashboard.html")
	
	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}
	
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()
	
	return tmpl.Execute(file, report)
}

//================================================================================
// Main Function
//================================================================================

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run main.go <log-directory> [output-file]")
		fmt.Println("Example: go run main.go ../logs reports/dashboard.html")
		os.Exit(1)
	}
	
	logDir := os.Args[1]
	outputFile := "reports/dashboard.html"
	if len(os.Args) > 2 {
		outputFile = os.Args[2]
	}
	
	analyzer := NewLogAnalyzer()
	
	log.Printf("Starting log analysis...")
	if err := analyzer.LoadLogs(logDir); err != nil {
		log.Fatalf("Failed to load logs: %v", err)
	}
	
	log.Printf("Generating analysis report...")
	report := analyzer.GenerateReport()
	
	log.Printf("Analysis Summary:")
	log.Printf("- Total entries: %d", report.TotalEntries)
	log.Printf("- Total sessions: %d", report.TotalSessions)
	log.Printf("- Total searches: %d", report.TotalSearches)
	log.Printf("- Zero result rate: %.1f%%", report.ZeroResultRate)
	log.Printf("- Cache hit rate: %.1f%%", report.CacheHitRate)
	log.Printf("- Average duration: %v", report.AvgDuration)
	
	log.Printf("Generating HTML report: %s", outputFile)
	if err := os.MkdirAll(filepath.Dir(outputFile), 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}
	
	if err := analyzer.GenerateHTMLReport(report, outputFile); err != nil {
		log.Fatalf("Failed to generate HTML report: %v", err)
	}
	
	log.Printf("✅ Analysis complete! Report saved to: %s", outputFile)
}