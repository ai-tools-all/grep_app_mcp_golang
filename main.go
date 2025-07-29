package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/google/go-github/v58/github"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

//================================================================================
// Version Variables (injected at build time)
//================================================================================

var (
	Version   = "1.0.0-go"        // Injected via -ldflags "-X main.Version=..."
	GitCommit = "unknown"         // Injected via -ldflags "-X main.GitCommit=..."
	BuildDate = "unknown"         // Injected via -ldflags "-X main.BuildDate=..."
	BuildBy   = "unknown"         // Injected via -ldflags "-X main.BuildBy=..."
)

//================================================================================
// Constants
//================================================================================

const (
	grepAppAPIBaseURL = "https://grep.app/api/search"
	cacheDir          = "./cache"
	cacheTTL          = 24 * time.Hour
	maxSearchPages    = 5 // To prevent excessive API calls, matching the TS implementation
)

//================================================================================
// Data Structures
//================================================================================

// GrepAppResponse mirrors the JSON structure from the grep.app API.
type GrepAppResponse struct {
	Hits struct {
		Hits []struct {
			Repo struct {
				Raw string `json:"raw"`
			} `json:"repo"`
			Path struct {
				Raw string `json:"raw"`
			} `json:"path"`
			Content struct {
				Snippet string `json:"snippet"`
			} `json:"content"`
		} `json:"hits"`
	} `json:"hits"`
	Facets struct {
		Count int `json:"count"`
		Pages int `json:"pages"`
	} `json:"facets"`
}

// Hits stores the structured search results.
// It maps repository -> file path -> line number -> line content.
type Hits struct {
	Hits map[string]map[string]map[string]string `json:"hits"`
}

// CacheEntry wraps data stored in the cache with a timestamp.
type CacheEntry[T any] struct {
	Data      T         `json:"data"`
	Timestamp time.Time `json:"timestamp"`
	Query     string    `json:"query"`
}

// NumberedHit is used for flattening search results for batch retrieval.
type NumberedHit struct {
	Number int
	Repo   string
	Path   string
}

// GitHubFileRequest specifies a file to be fetched from GitHub.
type GitHubFileRequest struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	Path  string `json:"path"`
}

// RetrievedFile holds the content or an error for a file fetched from GitHub.
type RetrievedFile struct {
	Number  int    `json:"number"`
	Repo    string `json:"repo"`
	Path    string `json:"path"`
	Content string `json:"content"`
	Error   string `json:"error,omitempty"`
}

// BatchRetrievalResult encapsulates the outcome of a batch file retrieval operation.
type BatchRetrievalResult struct {
	Success bool            `json:"success"`
	Files   []RetrievedFile `json:"files"`
	Error   string          `json:"error,omitempty"`
}

//================================================================================
// Caching Logic
//================================================================================

// generateCacheKey creates an MD5 hash for a given query and page number.
func generateCacheKey(keyObj map[string]interface{}) string {
	keyBytes, _ := json.Marshal(keyObj)
	hash := md5.Sum(keyBytes)
	return hex.EncodeToString(hash[:])
}

// getCachedData retrieves and unmarshals data from a cache file if it exists and is not expired.
func getCachedData[T any](cacheKey string) (*T, error) {
	filePath := filepath.Join(cacheDir, cacheKey+".json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil, nil // Cache miss
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read cache file: %w", err)
	}

	var entry CacheEntry[T]
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cache entry: %w", err)
	}

	if time.Since(entry.Timestamp) > cacheTTL {
		log.Printf("Cache expired for key: %s", cacheKey)
		os.Remove(filePath) // Delete expired cache file
		return nil, nil     // Cache miss
	}

	log.Printf("Cache hit for key: %s", cacheKey)
	return &entry.Data, nil
}

// cacheData marshals and writes data to a cache file.
func cacheData[T any](cacheKey string, data T, query string) error {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	entry := CacheEntry[T]{
		Data:      data,
		Timestamp: time.Now(),
		Query:     query,
	}

	entryBytes, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal cache entry: %w", err)
	}

	filePath := filepath.Join(cacheDir, cacheKey+".json")
	return os.WriteFile(filePath, entryBytes, 0644)
}

// findCacheFiles searches the cache directory for files matching a specific query.
func findCacheFiles(query string) ([]string, error) {
	files, err := os.ReadDir(cacheDir)
	if err != nil {
		return nil, err
	}
	var matchingFiles []string
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		filePath := filepath.Join(cacheDir, file.Name())
		content, err := os.ReadFile(filePath)
		if err != nil {
			continue // Skip unreadable files
		}
		var entry CacheEntry[json.RawMessage]
		if err := json.Unmarshal(content, &entry); err != nil {
			continue // Skip unparseable files
		}
		if entry.Query == query {
			matchingFiles = append(matchingFiles, file.Name())
		}
	}
	return matchingFiles, nil
}

// getQueryResults loads the most recent, complete cached search result for a query.
type fullSearchResult struct {
	Hits  Hits `json:"hits"`
	Count int  `json:"count"`
}

func getQueryResults(query string) (*Hits, error) {
	cacheKey := generateCacheKey(map[string]interface{}{"query": query, "complete": true})
	cached, err := getCachedData[fullSearchResult](cacheKey)
	if err != nil {
		log.Printf("Error reading cache for complete query results: %v", err)
		return nil, err
	}
	if cached != nil {
		return &cached.Hits, nil
	}
	return nil, nil // Not found
}

//================================================================================
// Regex Support Functions
//================================================================================

// RegexValidationResult holds regex validation results
type RegexValidationResult struct {
	IsValid     bool
	CompiledRe  *regexp.Regexp
	Error       error
	Pattern     string
}

// validateRegexPattern validates and compiles a regex pattern
func validateRegexPattern(pattern string) *RegexValidationResult {
	if pattern == "" {
		return &RegexValidationResult{
			IsValid: false,
			Error:   fmt.Errorf("empty regex pattern"),
			Pattern: pattern,
		}
	}

	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return &RegexValidationResult{
			IsValid: false,
			Error:   fmt.Errorf("invalid regex pattern: %w", err),
			Pattern: pattern,
		}
	}

	return &RegexValidationResult{
		IsValid:    true,
		CompiledRe: compiled,
		Error:      nil,
		Pattern:    pattern,
	}
}

// applyRegexFilter applies regex filtering to search results
func applyRegexFilter(hits *Hits, regexResult *RegexValidationResult) *Hits {
	if !regexResult.IsValid || regexResult.CompiledRe == nil {
		return hits
	}

	filteredHits := &Hits{Hits: make(map[string]map[string]map[string]string)}
	
	for repo, pathData := range hits.Hits {
		for path, lines := range pathData {
			filteredLines := make(map[string]string)
			
			for lineNum, line := range lines {
				if regexResult.CompiledRe.MatchString(line) {
					filteredLines[lineNum] = line
				}
			}
			
			if len(filteredLines) > 0 {
				if filteredHits.Hits[repo] == nil {
					filteredHits.Hits[repo] = make(map[string]map[string]string)
				}
				filteredHits.Hits[repo][path] = filteredLines
			}
		}
	}
	
	return filteredHits
}

//================================================================================
// Core Logic (grep.app, GitHub, Batch)
//================================================================================

// parseSnippet extracts line numbers and code from the HTML snippet returned by grep.app.
func parseSnippet(snippet string) (map[string]string, error) {
	matches := make(map[string]string)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(snippet))
	if err != nil {
		return nil, err
	}

	doc.Find("tr").Each(func(i int, tr *goquery.Selection) {
		lineNum := tr.Find("div.lineno").Text()
		linePre := tr.Find("pre")
		if lineNum != "" && linePre.Find("mark").Length() > 0 {
			matches[strings.TrimSpace(lineNum)] = strings.TrimSpace(linePre.Text())
		}
	})
	return matches, nil
}

// mergeHits combines search results from a source Hits object into a target.
func mergeHits(target, source *Hits) {
	if target.Hits == nil {
		target.Hits = make(map[string]map[string]map[string]string)
	}
	for repo, pathData := range source.Hits {
		if _, ok := target.Hits[repo]; !ok {
			target.Hits[repo] = make(map[string]map[string]string)
		}
		for path, lines := range pathData {
			if _, ok := target.Hits[repo][path]; !ok {
				target.Hits[repo][path] = make(map[string]string)
			}
			for lineNum, line := range lines {
				target.Hits[repo][path][lineNum] = line
			}
		}
	}
}

// fetchGrepAppPage fetches a single page of results from the grep.app API, using cache if available.
func fetchGrepAppPage(ctx context.Context, client *http.Client, args map[string]interface{}, page int) (*GrepAppResponse, error) {
	query, _ := args["query"].(string)
	cacheKey := generateCacheKey(map[string]interface{}{"query": query, "page": page})

	log.Printf("Fetching page %d for query: %s", page, query)

	// Check cache
	cached, err := getCachedData[GrepAppResponse](cacheKey)
	if err != nil {
		log.Printf("Cache read error for key %s: %v", cacheKey, err)
	}
	if cached != nil {
		log.Printf("Cache hit for query '%s', page %d", query, page)
		
		// Log cache hit
		if logger := GetLogger(); logger != nil {
			logger.LogCacheOperation(cacheKey, true, query)
		}
		
		return cached, nil
	}

	log.Printf("Cache miss for query '%s', page %d - fetching from API", query, page)
	
	// Log cache miss
	if logger := GetLogger(); logger != nil {
		logger.LogCacheOperation(cacheKey, false, query)
	}

	// Fetch from API
	reqURL, _ := url.Parse(grepAppAPIBaseURL)
	q := reqURL.Query()
	q.Set("q", query)
	q.Set("page", strconv.Itoa(page))
	if v, ok := args["caseSensitive"].(bool); ok && v {
		q.Set("case", "1")
	}
	if v, ok := args["useRegex"].(bool); ok && v {
		q.Set("regexp", "1")
	}
	if v, ok := args["wholeWords"].(bool); ok && v {
		q.Set("words", "1")
	}
	if v, ok := args["repoFilter"].(string); ok && v != "" {
		q.Set("repo", v)
	}
	if v, ok := args["pathFilter"].(string); ok && v != "" {
		q.Set("path", v)
	}
	if v, ok := args["langFilter"].(string); ok && v != "" {
		q.Set("lang", v)
	}
	reqURL.RawQuery = q.Encode()

	log.Printf("Making HTTP request to: %s", reqURL.String())

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL.String(), nil)
	if err != nil {
		log.Printf("Failed to create HTTP request: %v", err)
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	start := time.Now()
	resp, err := client.Do(req)
	duration := time.Since(start)

	// Log API request
	if logger := GetLogger(); logger != nil {
		logger.LogAPIRequest(reqURL.String(), duration, 0, err)
	}

	if err != nil {
		log.Printf("HTTP request failed after %v: %v", duration, err)
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("HTTP request completed in %v, status: %d", duration, resp.StatusCode)
	
	// Update API request log with status code
	if logger := GetLogger(); logger != nil {
		logger.LogAPIRequest(reqURL.String(), duration, resp.StatusCode, nil)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("API request failed with status %d, body: %s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var apiResponse GrepAppResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		log.Printf("Failed to decode API response: %v", err)
		return nil, fmt.Errorf("failed to decode API response: %w", err)
	}

	log.Printf("Successfully parsed API response: %d hits, %d total results", len(apiResponse.Hits.Hits), apiResponse.Facets.Count)

	// Save to cache
	if err := cacheData(cacheKey, apiResponse, query); err != nil {
		log.Printf("Cache write error for key %s: %v", cacheKey, err)
	} else {
		log.Printf("Successfully cached response for query '%s', page %d", query, page)
	}

	return &apiResponse, nil
}

// flattenHits converts the nested Hits map into a simple numbered list.
func flattenHits(hits *Hits) []NumberedHit {
	var flattened []NumberedHit
	i := 1
	// Sort repos and paths for deterministic numbering
	var repos []string
	for repo := range hits.Hits {
		repos = append(repos, repo)
	}
	sort.Strings(repos)

	for _, repo := range repos {
		pathData := hits.Hits[repo]
		var paths []string
		for path := range pathData {
			paths = append(paths, path)
		}
		sort.Strings(paths)

		for _, path := range paths {
			flattened = append(flattened, NumberedHit{
				Number: i,
				Repo:   repo,
				Path:   path,
			})
			i++
		}
	}
	return flattened
}

// parseGitHubRepo extracts owner and repo from a GitHub repository string.
var githubRepoRegex = regexp.MustCompile(`^(?:https?:\/\/github\.com\/)?([\w.-]+)\/([\w.-]+)(?:\.git)?$`)

func parseGitHubRepo(repoString string) (owner, repo string, err error) {
	matches := githubRepoRegex.FindStringSubmatch(repoString)
	if len(matches) != 3 {
		return "", "", fmt.Errorf("invalid GitHub repo format: %s", repoString)
	}
	return matches[1], matches[2], nil
}

// fetchGitHubFiles retrieves multiple files from GitHub concurrently.
func fetchGitHubFiles(ctx context.Context, ghClient *github.Client, requests []GitHubFileRequest) []RetrievedFile {
	log.Printf("🔗 Starting GitHub file retrieval for %d files", len(requests))
	start := time.Now()

	var wg sync.WaitGroup
	resultsChan := make(chan RetrievedFile, len(requests))

	for i, req := range requests {
		wg.Add(1)
		go func(req GitHubFileRequest, num int) {
			defer wg.Done()

			repoPath := fmt.Sprintf("%s/%s", req.Owner, req.Repo)
			log.Printf("📁 Fetching file %d: %s/%s", num, repoPath, req.Path)

			fileStart := time.Now()
			fileContent, _, _, err := ghClient.Repositories.GetContents(ctx, req.Owner, req.Repo, req.Path, nil)
			fileDuration := time.Since(fileStart)

			if err != nil {
				log.Printf("❌ Failed to fetch file %d (%s/%s) after %v: %v", num, repoPath, req.Path, fileDuration, err)
				resultsChan <- RetrievedFile{Number: num, Repo: repoPath, Path: req.Path, Error: err.Error()}
				return
			}
			if fileContent == nil {
				log.Printf("❌ File %d (%s/%s) returned nil content after %v", num, repoPath, req.Path, fileDuration)
				resultsChan <- RetrievedFile{Number: num, Repo: repoPath, Path: req.Path, Error: "file content is nil"}
				return
			}
			content, err := fileContent.GetContent()
			if err != nil {
				log.Printf("❌ Failed to decode file %d (%s/%s) after %v: %v", num, repoPath, req.Path, fileDuration, err)
				resultsChan <- RetrievedFile{Number: num, Repo: repoPath, Path: req.Path, Error: fmt.Sprintf("failed to get file content: %v", err)}
				return
			}

			log.Printf("✅ Successfully fetched file %d (%s/%s) in %v (%d bytes)", num, repoPath, req.Path, fileDuration, len(content))
			resultsChan <- RetrievedFile{Number: num, Repo: repoPath, Path: req.Path, Content: content}
		}(req, i+1) // Use index for temporary numbering before matching with original
	}

	wg.Wait()
	close(resultsChan)

	var results []RetrievedFile
	successCount := 0
	errorCount := 0

	for res := range resultsChan {
		results = append(results, res)
		if res.Error == "" {
			successCount++
		} else {
			errorCount++
		}
	}

	duration := time.Since(start)
	log.Printf("🎯 GitHub file retrieval completed in %v: %d successful, %d errors", duration, successCount, errorCount)

	return results
}

// batchRetrieveFiles orchestrates the batch retrieval process.
func batchRetrieveFiles(ctx context.Context, ghClient *github.Client, query string, resultNumbers []int) (*BatchRetrievalResult, error) {
	log.Printf("🔄 Starting batch file retrieval process for query: '%s'", query)

	cachedHits, err := getQueryResults(query)
	if err != nil {
		log.Printf("❌ Failed to get cached query results: %v", err)
		return nil, fmt.Errorf("failed to get cached query results: %w", err)
	}
	if cachedHits == nil {
		log.Printf("⚠️ No cached results found for query: '%s'", query)
		return &BatchRetrievalResult{Success: false, Error: "No cached results found for query: " + query}, nil
	}

	log.Printf("✅ Found cached results for query: '%s'", query)

	allNumberedHits := flattenHits(cachedHits)
	hitsToProcess := allNumberedHits

	if len(resultNumbers) > 0 {
		log.Printf("🔢 Filtering to specific result numbers: %v", resultNumbers)
		var filteredHits []NumberedHit
		numberSet := make(map[int]struct{})
		for _, n := range resultNumbers {
			numberSet[n] = struct{}{}
		}
		for _, hit := range allNumberedHits {
			if _, ok := numberSet[hit.Number]; ok {
				filteredHits = append(filteredHits, hit)
			}
		}
		hitsToProcess = filteredHits
		log.Printf("🎯 Filtered to %d specific results from %d total", len(hitsToProcess), len(allNumberedHits))
	} else {
		log.Printf("📊 Processing all %d available results", len(allNumberedHits))
	}

	if len(hitsToProcess) == 0 {
		log.Printf("❌ No results found for the given result numbers")
		return &BatchRetrievalResult{Success: false, Error: "No results found for the given result numbers."}, nil
	}

	var fileRequests []GitHubFileRequest
	requestNumberMap := make(map[int]int)
	skipCount := 0

	log.Printf("🔍 Preparing GitHub file requests for %d hits", len(hitsToProcess))

	for i, hit := range hitsToProcess {
		owner, repo, err := parseGitHubRepo(hit.Repo)
		if err != nil {
			log.Printf("⚠️ Skipping invalid repo format: %s (error: %v)", hit.Repo, err)
			skipCount++
			continue
		}
		fileRequests = append(fileRequests, GitHubFileRequest{Owner: owner, Repo: repo, Path: hit.Path})
		requestNumberMap[i+1-skipCount] = hit.Number
	}

	if skipCount > 0 {
		log.Printf("⚠️ Skipped %d invalid repositories", skipCount)
	}

	log.Printf("📋 Created %d GitHub file requests", len(fileRequests))

	ghResults := fetchGitHubFiles(ctx, ghClient, fileRequests)

	log.Printf("🔄 Mapping results back to original numbering")
	finalFiles := make([]RetrievedFile, len(ghResults))
	for i, file := range ghResults {
		finalFiles[i] = file
		finalFiles[i].Number = requestNumberMap[file.Number]
	}

	sort.Slice(finalFiles, func(i, j int) bool {
		return finalFiles[i].Number < finalFiles[j].Number
	})

	log.Printf("✅ Batch retrieval process completed: %d files processed", len(finalFiles))

	return &BatchRetrievalResult{Success: true, Files: finalFiles}, nil
}

//================================================================================
// Formatting Logic
//================================================================================

// formatResultsAsText creates a human-readable summary of search results.
func formatResultsAsText(hits *Hits) string {
	var b strings.Builder
	separator := strings.Repeat("─", 80) + "\n"
	repoCt, fileCt, lineCt := 0, 0, 0

	// Sort for deterministic output
	var repos []string
	for repo := range hits.Hits {
		repos = append(repos, repo)
	}
	sort.Strings(repos)

	for _, repo := range repos {
		repoCt++
		b.WriteString(separator)
		fmt.Fprintf(&b, "Repository: %s\n", repo)

		pathData := hits.Hits[repo]
		var paths []string
		for path := range pathData {
			paths = append(paths, path)
		}
		sort.Strings(paths)

		for _, path := range paths {
			fileCt++
			fmt.Fprintf(&b, "  /%s\n", path)

			lines := pathData[path]
			var lineNums []int
			for lineNumStr := range lines {
				num, _ := strconv.Atoi(lineNumStr)
				lineNums = append(lineNums, num)
			}
			sort.Ints(lineNums)

			for _, lineNum := range lineNums {
				lineCt++
				lineNumStr := strconv.Itoa(lineNum)
				fmt.Fprintf(&b, "    %5s: %s\n", lineNumStr, lines[lineNumStr])
			}
		}
	}
	b.WriteString(separator)
	fmt.Fprintf(&b, "Summary: Found %d matched lines in %d files across %d repositories.\n", lineCt, fileCt, repoCt)
	return b.String()
}

// formatResultsAsNumberedList creates a numbered list of files with their matches.
func formatResultsAsNumberedList(hits *Hits) string {
	var b strings.Builder
	numberedHits := flattenHits(hits)

	for _, hit := range numberedHits {
		pathData := hits.Hits[hit.Repo][hit.Path]
		var lineNums []int
		for lineNumStr := range pathData {
			num, _ := strconv.Atoi(lineNumStr)
			lineNums = append(lineNums, num)
		}
		sort.Ints(lineNums)

		if len(lineNums) > 0 {
			firstLineNumStr := strconv.Itoa(lineNums[0])
			fmt.Fprintf(&b, "%d. [%s/%s:%s] %s\n", hit.Number, hit.Repo, hit.Path, firstLineNumStr, pathData[firstLineNumStr])

			for i := 1; i < len(lineNums); i++ {
				lineNumStr := strconv.Itoa(lineNums[i])
				fmt.Fprintf(&b, "   L%s: %s\n", lineNumStr, pathData[lineNumStr])
			}
		}
	}
	return b.String()
}

//================================================================================
// Main Server Logic
//================================================================================

func main() {
	var transport string
	var port int
	flag.StringVar(&transport, "transport", "stdio", "Transport type (stdio or http)")
	flag.IntVar(&port, "port", 8603, "Port for http transport")
	flag.Parse()

	log.Printf("🚀 Initializing GrepApp MCP Server %s", Version)
	log.Printf("🔧 Configuration: transport=%s, port=%d", transport, port)
	log.Printf("📦 Build info: commit=%s, date=%s, by=%s", GitCommit, BuildDate, BuildBy)

	// Initialize observability logging
	log.Printf("📊 Initializing observability logging")
	if err := InitGlobalLogger(logDir); err != nil {
		log.Fatalf("💥 Failed to initialize logger: %v", err)
	}
	defer CloseGlobalLogger()

	// Initialize HTTP and GitHub clients
	log.Printf("🌐 Initializing HTTP client with 30s timeout")
	httpClient := &http.Client{Timeout: 30 * time.Second}

	log.Printf("🐙 Initializing GitHub client")
	ghClient := github.NewClient(nil)

	log.Printf("⚙️ Creating MCP server with tool capabilities and recovery")
	s := server.NewMCPServer(
		"GrepApp Search Server",
		Version,
		server.WithToolCapabilities(true),
		server.WithRecovery(),
	)

	// --- searchCode Tool ---
	log.Printf("🔧 Registering searchCode tool")
	searchCodeTool := mcp.NewTool("searchCode",
		mcp.WithDescription("Searches public code on GitHub using the grep.app API with enhanced regex support."),
		mcp.WithString("query", mcp.Description("The search query string. If useRegex is true, this should be a valid Go regex pattern."), mcp.Required()),
		mcp.WithBoolean("jsonOutput", mcp.Description("If true, return results as a JSON object.")),
		mcp.WithBoolean("numberedOutput", mcp.Description("If true, return results as a numbered list for model selection.")),
		mcp.WithBoolean("caseSensitive", mcp.Description("Perform a case-sensitive search.")),
		mcp.WithBoolean("useRegex", mcp.Description("Treat the query as a regular expression. Supports Go regex syntax with client-side validation and filtering.")),
		mcp.WithBoolean("wholeWords", mcp.Description("Search for whole words only.")),
		mcp.WithString("repoFilter", mcp.Description("Filter by repository name pattern.")),
		mcp.WithString("pathFilter", mcp.Description("Filter by file path pattern.")),
		mcp.WithString("langFilter", mcp.Description("Filter by language, comma-separated.")),
	)

	s.AddTool(searchCodeTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		query, _ := args["query"].(string)
		useRegex, _ := args["useRegex"].(bool)
		
		log.Printf("🔍 Starting searchCode tool execution for query: '%s', useRegex: %t", query, useRegex)
		log.Printf("📋 Tool arguments: %+v", args)

		// Log search start
		if logger := GetLogger(); logger != nil {
			logger.LogSearchStart(query, args)
		}

		// Validate regex pattern if useRegex is enabled
		var regexResult *RegexValidationResult
		if useRegex {
			log.Printf("🔧 Validating regex pattern: '%s'", query)
			regexResult = validateRegexPattern(query)
			if !regexResult.IsValid {
				log.Printf("❌ Invalid regex pattern: %v", regexResult.Error)
				return mcp.NewToolResultError(fmt.Sprintf("Invalid regex pattern: %v", regexResult.Error)), nil
			}
			log.Printf("✅ Regex pattern validated successfully")
		}

		start := time.Now()
		page := 1
		allHits := &Hits{}
		totalCount := 0
		apiRequests := 0

		log.Printf("📄 Beginning page-by-page search (max %d pages)", maxSearchPages)

		for {
			log.Printf("📖 Processing page %d", page)
			results, err := fetchGrepAppPage(ctx, httpClient, args, page)
			apiRequests++
			if err != nil {
				log.Printf("❌ searchCode tool failed on page %d: %v", page, err)
				
				// Log search failure
				if logger := GetLogger(); logger != nil {
					searchData := SearchLogData{
						Query:        query,
						UseRegex:     useRegex,
						Success:      false,
						Error:        err.Error(),
						Duration:     time.Since(start),
						APIRequests:  apiRequests,
						PagesScanned: page,
					}
					logger.LogSearchComplete(searchData)
				}
				
				return mcp.NewToolResultError(fmt.Sprintf("API fetch failed: %v", err)), nil
			}

			pageHits := &Hits{Hits: make(map[string]map[string]map[string]string)}
			snippetErrors := 0

			for _, hit := range results.Hits.Hits {
				parsed, err := parseSnippet(hit.Content.Snippet)
				if err != nil {
					snippetErrors++
					log.Printf("⚠️ Failed to parse snippet for repo %s/%s: %v", hit.Repo.Raw, hit.Path.Raw, err)
					continue
				}
				if pageHits.Hits[hit.Repo.Raw] == nil {
					pageHits.Hits[hit.Repo.Raw] = make(map[string]map[string]string)
				}
				if pageHits.Hits[hit.Repo.Raw][hit.Path.Raw] == nil {
					pageHits.Hits[hit.Repo.Raw][hit.Path.Raw] = make(map[string]string)
				}
				for lineNum, line := range parsed {
					pageHits.Hits[hit.Repo.Raw][hit.Path.Raw][lineNum] = line
				}
			}

			if snippetErrors > 0 {
				log.Printf("⚠️ Page %d had %d snippet parsing errors", page, snippetErrors)
			}

			log.Printf("✅ Page %d processed: %d repositories found", page, len(pageHits.Hits))

			mergeHits(allHits, pageHits)
			totalCount = results.Facets.Count

			log.Printf("📊 Total progress: %d repos collected, %d total results available", len(allHits.Hits), totalCount)

			if page >= results.Facets.Pages || page >= maxSearchPages {
				log.Printf("🏁 Search complete: reached page limit (page %d, max pages: %d, search limit: %d)", page, results.Facets.Pages, maxSearchPages)
				break
			}
			page++
		}

		duration := time.Since(start)

		if len(allHits.Hits) == 0 {
			log.Printf("📭 No results found for query '%s' after %v", query, duration)
			
			// Log zero results
			if logger := GetLogger(); logger != nil {
				filters := make(map[string]string)
				if v, ok := args["repoFilter"].(string); ok && v != "" {
					filters["repo"] = v
				}
				if v, ok := args["pathFilter"].(string); ok && v != "" {
					filters["path"] = v
				}
				if v, ok := args["langFilter"].(string); ok && v != "" {
					filters["lang"] = v
				}
				
				caseSensitive, _ := args["caseSensitive"].(bool)
				wholeWords, _ := args["wholeWords"].(bool)
				
				searchData := SearchLogData{
					Query:         query,
					UseRegex:      useRegex,
					CaseSensitive: caseSensitive,
					WholeWords:    wholeWords,
					Filters:       filters,
					ResultCount:   0,
					FileCount:     0,
					LineCount:     0,
					Duration:      duration,
					Success:       true,
					APIRequests:   apiRequests,
					PagesScanned:  page - 1,
				}
				logger.LogSearchComplete(searchData)
			}
			
			return mcp.NewToolResultText("No results found for your query."), nil
		}

		// Apply regex filtering if enabled
		if useRegex && regexResult != nil && regexResult.IsValid {
			log.Printf("🔍 Applying client-side regex filtering")
			originalHits := len(allHits.Hits)
			allHits = applyRegexFilter(allHits, regexResult)
			log.Printf("🎯 Regex filtering complete: %d repos after filtering (was %d)", len(allHits.Hits), originalHits)
			
			if len(allHits.Hits) == 0 {
				log.Printf("📭 No results matched regex pattern after filtering")
				
				// Log regex filtered zero results
				if logger := GetLogger(); logger != nil {
					filters := make(map[string]string)
					if v, ok := args["repoFilter"].(string); ok && v != "" {
						filters["repo"] = v
					}
					if v, ok := args["pathFilter"].(string); ok && v != "" {
						filters["path"] = v
					}
					if v, ok := args["langFilter"].(string); ok && v != "" {
						filters["lang"] = v
					}
					
					caseSensitive, _ := args["caseSensitive"].(bool)
					wholeWords, _ := args["wholeWords"].(bool)
					
					searchData := SearchLogData{
						Query:         query,
						UseRegex:      useRegex,
						CaseSensitive: caseSensitive,
						WholeWords:    wholeWords,
						Filters:       filters,
						ResultCount:   0,
						FileCount:     0,
						LineCount:     0,
						Duration:      duration,
						Success:       true,
						APIRequests:   apiRequests,
						PagesScanned:  page - 1,
						RegexFiltered: true,
					}
					logger.LogSearchComplete(searchData)
				}
				
				return mcp.NewToolResultText("No results matched the regex pattern."), nil
			}
		}

		// Count final results
		totalFiles := 0
		totalLines := 0
		for _, repoData := range allHits.Hits {
			for _, fileData := range repoData {
				totalFiles++
				totalLines += len(fileData)
			}
		}

		log.Printf("🎯 Search completed successfully in %v: %d repos, %d files, %d matched lines", duration, len(allHits.Hits), totalFiles, totalLines)

		// Log successful search completion
		if logger := GetLogger(); logger != nil {
			filters := make(map[string]string)
			if v, ok := args["repoFilter"].(string); ok && v != "" {
				filters["repo"] = v
			}
			if v, ok := args["pathFilter"].(string); ok && v != "" {
				filters["path"] = v
			}
			if v, ok := args["langFilter"].(string); ok && v != "" {
				filters["lang"] = v
			}
			
			caseSensitive, _ := args["caseSensitive"].(bool)
			wholeWords, _ := args["wholeWords"].(bool)
			
			searchData := SearchLogData{
				Query:         query,
				UseRegex:      useRegex,
				CaseSensitive: caseSensitive,
				WholeWords:    wholeWords,
				Filters:       filters,
				ResultCount:   len(allHits.Hits),
				FileCount:     totalFiles,
				LineCount:     totalLines,
				Duration:      duration,
				Success:       true,
				APIRequests:   apiRequests,
				PagesScanned:  page - 1,
				RegexFiltered: useRegex && regexResult != nil && regexResult.IsValid,
			}
			logger.LogSearchComplete(searchData)
		}

		// Cache the complete result for batch retrieval
		completeCacheKey := generateCacheKey(map[string]interface{}{"query": query, "complete": true})
		fullRes := fullSearchResult{Hits: *allHits, Count: totalCount}
		if err := cacheData(completeCacheKey, fullRes, query); err != nil {
			log.Printf("⚠️ Failed to cache complete results: %v", err)
		} else {
			log.Printf("💾 Successfully cached complete results for future batch retrieval")
		}

		// Format output
		if jsonOutput, _ := args["jsonOutput"].(bool); jsonOutput {
			log.Printf("📤 Returning JSON output format")
			jsonBytes, err := json.MarshalIndent(allHits.Hits, "", "  ")
			if err != nil {
				log.Printf("❌ JSON marshaling failed: %v", err)
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal JSON: %v", err)), nil
			}
			return mcp.NewToolResultText(string(jsonBytes)), nil
		}
		if numberedOutput, _ := args["numberedOutput"].(bool); numberedOutput {
			log.Printf("📤 Returning numbered list output format")
			return mcp.NewToolResultText(formatResultsAsNumberedList(allHits)), nil
		}

		log.Printf("📤 Returning formatted text output")
		return mcp.NewToolResultText(formatResultsAsText(allHits)), nil
	})

	// --- batchRetrievalTool ---
	log.Printf("🔧 Registering batchRetrievalTool")
	batchRetrievalTool := mcp.NewTool("batchRetrievalTool",
		mcp.WithDescription("Retrieve file contents for specified search results from a cached query."),
		mcp.WithString("query", mcp.Description("The original search query."), mcp.Required()),
		mcp.WithArray("resultNumbers", mcp.Description("List of result numbers to retrieve.")),
	)

	s.AddTool(batchRetrievalTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		log.Printf("📦 Starting batchRetrievalTool execution")
		log.Printf("📋 Tool arguments: %+v", args)

		start := time.Now()

		query, ok := args["query"].(string)
		if !ok || query == "" {
			log.Printf("❌ batchRetrievalTool failed: missing query parameter")
			return mcp.NewToolResultError("query parameter is required"), nil
		}

		var resultNumbers []int
		if nums, ok := args["resultNumbers"].([]interface{}); ok {
			for _, n := range nums {
				if numFloat, ok := n.(float64); ok {
					resultNumbers = append(resultNumbers, int(numFloat))
				}
			}
		}

		// Log batch retrieval start
		if logger := GetLogger(); logger != nil {
			logger.LogBatchRetrievalStart(query, resultNumbers)
		}

		log.Printf("🔍 Retrieving files for query: '%s', result numbers: %v", query, resultNumbers)

		result, err := batchRetrieveFiles(ctx, ghClient, query, resultNumbers)
		duration := time.Since(start)
		
		if err != nil {
			log.Printf("❌ batchRetrievalTool failed after %v: %v", duration, err)
			
			// Log batch retrieval failure
			if logger := GetLogger(); logger != nil {
				batchData := BatchRetrievalLogData{
					Query:         query,
					RequestedNums: resultNumbers,
					Duration:      duration,
					Success:       false,
					Error:         err.Error(),
				}
				logger.LogBatchRetrievalComplete(batchData)
			}
			
			return mcp.NewToolResultError(fmt.Sprintf("batch retrieval failed: %v", err)), nil
		}

		// Count success/error files
		successCount := 0
		errorCount := 0
		for _, file := range result.Files {
			if file.Error == "" {
				successCount++
			} else {
				errorCount++
			}
		}

		// Log batch retrieval completion
		if logger := GetLogger(); logger != nil {
			batchData := BatchRetrievalLogData{
				Query:         query,
				RequestedNums: resultNumbers,
				FilesFound:    len(result.Files),
				FilesSuccess:  successCount,
				FilesError:    errorCount,
				Duration:      duration,
				Success:       result.Success,
				Error:         result.Error,
			}
			logger.LogBatchRetrievalComplete(batchData)
		}

		if result.Success {
			log.Printf("🎯 batchRetrievalTool completed successfully in %v: %d files retrieved, %d errors", duration, successCount, errorCount)
		} else {
			log.Printf("⚠️ batchRetrievalTool completed with errors in %v: %s", duration, result.Error)
		}

		resultBytes, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			log.Printf("❌ JSON marshaling failed: %v", err)
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}

		log.Printf("📤 Returning batch retrieval results")
		return mcp.NewToolResultText(string(resultBytes)), nil
	})

	// --- Start Server ---
	if transport == "http" {
		log.Printf("🚀 Starting HTTP server mode")
		httpServer := server.NewStreamableHTTPServer(s)
		addr := fmt.Sprintf(":%d", port)
		log.Printf("🌐 HTTP server listening on %s/mcp", addr)
		log.Printf("📊 Server ready to handle MCP requests")
		if err := httpServer.Start(addr); err != nil {
			log.Fatalf("💥 Server startup failed: %v", err)
		}
	} else {
		log.Printf("🚀 Starting STDIO server mode")
		log.Printf("📊 Server ready to handle MCP requests via stdin/stdout")
		if err := server.ServeStdio(s); err != nil {
			log.Fatalf("💥 Server startup failed: %v", err)
		}
	}
}
