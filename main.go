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

	// Check cache
	cached, err := getCachedData[GrepAppResponse](cacheKey)
	if err != nil {
		log.Printf("Cache read error: %v", err) // Log but continue
	}
	if cached != nil {
		return cached, nil
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

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var apiResponse GrepAppResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return nil, fmt.Errorf("failed to decode API response: %w", err)
	}

	// Save to cache
	if err := cacheData(cacheKey, apiResponse, query); err != nil {
		log.Printf("Cache write error: %v", err)
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
	var wg sync.WaitGroup
	resultsChan := make(chan RetrievedFile, len(requests))

	for i, req := range requests {
		wg.Add(1)
		go func(req GitHubFileRequest, num int) {
			defer wg.Done()
			fileContent, _, _, err := ghClient.Repositories.GetContents(ctx, req.Owner, req.Repo, req.Path, nil)
			if err != nil {
				resultsChan <- RetrievedFile{Number: num, Repo: fmt.Sprintf("%s/%s", req.Owner, req.Repo), Path: req.Path, Error: err.Error()}
				return
			}
			if fileContent == nil {
				resultsChan <- RetrievedFile{Number: num, Repo: fmt.Sprintf("%s/%s", req.Owner, req.Repo), Path: req.Path, Error: "file content is nil"}
				return
			}
			content, err := fileContent.GetContent()
			if err != nil {
				resultsChan <- RetrievedFile{Number: num, Repo: fmt.Sprintf("%s/%s", req.Owner, req.Repo), Path: req.Path, Error: fmt.Sprintf("failed to get file content: %v", err)}
				return
			}
			resultsChan <- RetrievedFile{Number: num, Repo: fmt.Sprintf("%s/%s", req.Owner, req.Repo), Path: req.Path, Content: content}
		}(req, i+1) // Use index for temporary numbering before matching with original
	}

	wg.Wait()
	close(resultsChan)

	var results []RetrievedFile
	for res := range resultsChan {
		results = append(results, res)
	}
	return results
}

// batchRetrieveFiles orchestrates the batch retrieval process.
func batchRetrieveFiles(ctx context.Context, ghClient *github.Client, query string, resultNumbers []int) (*BatchRetrievalResult, error) {
	cachedHits, err := getQueryResults(query)
	if err != nil {
		return nil, fmt.Errorf("failed to get cached query results: %w", err)
	}
	if cachedHits == nil {
		return &BatchRetrievalResult{Success: false, Error: "No cached results found for query: " + query}, nil
	}

	allNumberedHits := flattenHits(cachedHits)
	hitsToProcess := allNumberedHits

	if len(resultNumbers) > 0 {
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
	}

	if len(hitsToProcess) == 0 {
		return &BatchRetrievalResult{Success: false, Error: "No results found for the given result numbers."}, nil
	}

	var fileRequests []GitHubFileRequest
	// Map to restore original numbering
	requestNumberMap := make(map[int]int)
	for i, hit := range hitsToProcess {
		owner, repo, err := parseGitHubRepo(hit.Repo)
		if err != nil {
			log.Printf("Skipping invalid repo format: %s", hit.Repo)
			continue
		}
		fileRequests = append(fileRequests, GitHubFileRequest{Owner: owner, Repo: repo, Path: hit.Path})
		requestNumberMap[i+1] = hit.Number
	}

	ghResults := fetchGitHubFiles(ctx, ghClient, fileRequests)

	// Map results back to their original numbers
	finalFiles := make([]RetrievedFile, len(ghResults))
	for i, file := range ghResults {
		finalFiles[i] = file
		finalFiles[i].Number = requestNumberMap[file.Number] // Remap index to original number
	}
	// Sort by original number
	sort.Slice(finalFiles, func(i, j int) bool {
		return finalFiles[i].Number < finalFiles[j].Number
	})

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

	log.Println("Initializing GrepApp MCP Server...")

	// Initialize HTTP and GitHub clients
	httpClient := &http.Client{Timeout: 30 * time.Second}
	ghClient := github.NewClient(nil)

	s := server.NewMCPServer(
		"GrepApp Search Server",
		"1.0.0-go",
		server.WithToolCapabilities(true),
		server.WithRecovery(),
	)

	// --- searchCode Tool ---
	searchCodeTool := mcp.NewTool("searchCode",
		mcp.WithDescription("Searches public code on GitHub using the grep.app API."),
		mcp.WithString("query", mcp.Description("The search query string."), mcp.Required()),
		mcp.WithBoolean("jsonOutput", mcp.Description("If true, return results as a JSON object.")),
		mcp.WithBoolean("numberedOutput", mcp.Description("If true, return results as a numbered list for model selection.")),
		mcp.WithBoolean("caseSensitive", mcp.Description("Perform a case-sensitive search.")),
		mcp.WithBoolean("useRegex", mcp.Description("Treat the query as a regular expression.")),
		mcp.WithBoolean("wholeWords", mcp.Description("Search for whole words only.")),
		mcp.WithString("repoFilter", mcp.Description("Filter by repository name pattern.")),
		mcp.WithString("pathFilter", mcp.Description("Filter by file path pattern.")),
		mcp.WithString("langFilter", mcp.Description("Filter by language, comma-separated.")),
	)

	s.AddTool(searchCodeTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		log.Printf("Executing searchCode tool with args: %+v", args)

		page := 1
		allHits := &Hits{}
		totalCount := 0

		for {
			results, err := fetchGrepAppPage(ctx, httpClient, args, page)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("API fetch failed: %v", err)), nil
			}

			pageHits := &Hits{Hits: make(map[string]map[string]map[string]string)}
			for _, hit := range results.Hits.Hits {
				parsed, err := parseSnippet(hit.Content.Snippet)
				if err != nil {
					log.Printf("Failed to parse snippet for repo %s: %v", hit.Repo.Raw, err)
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

			mergeHits(allHits, pageHits)
			totalCount = results.Facets.Count

			if page >= results.Facets.Pages || page >= maxSearchPages {
				break
			}
			page++
		}

		if len(allHits.Hits) == 0 {
			return mcp.NewToolResultText("No results found for your query."), nil
		}

		// Cache the complete result for batch retrieval
		query, _ := args["query"].(string)
		completeCacheKey := generateCacheKey(map[string]interface{}{"query": query, "complete": true})
		fullRes := fullSearchResult{Hits: *allHits, Count: totalCount}
		if err := cacheData(completeCacheKey, fullRes, query); err != nil {
			log.Printf("Failed to cache complete results: %v", err)
		}

		// Format output
		if jsonOutput, _ := args["jsonOutput"].(bool); jsonOutput {
			jsonBytes, err := json.MarshalIndent(allHits.Hits, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal JSON: %v", err)), nil
			}
			return mcp.NewToolResultText(string(jsonBytes)), nil
		}
		if numberedOutput, _ := args["numberedOutput"].(bool); numberedOutput {
			return mcp.NewToolResultText(formatResultsAsNumberedList(allHits)), nil
		}

		return mcp.NewToolResultText(formatResultsAsText(allHits)), nil
	})

	// --- batchRetrievalTool ---
	batchRetrievalTool := mcp.NewTool("batchRetrievalTool",
		mcp.WithDescription("Retrieve file contents for specified search results from a cached query."),
		mcp.WithString("query", mcp.Description("The original search query."), mcp.Required()),
		mcp.WithArray("resultNumbers", mcp.Description("List of result numbers to retrieve.")),
	)

	s.AddTool(batchRetrievalTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		log.Printf("Executing batchRetrievalTool with args: %+v", args)

		query, ok := args["query"].(string)
		if !ok || query == "" {
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

		result, err := batchRetrieveFiles(ctx, ghClient, query, resultNumbers)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("batch retrieval failed: %v", err)), nil
		}

		resultBytes, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}

		return mcp.NewToolResultText(string(resultBytes)), nil
	})

	// --- Start Server ---
	if transport == "http" {
		httpServer := server.NewStreamableHTTPServer(s)
		addr := fmt.Sprintf(":%d", port)
		log.Printf("HTTP server listening on %s/mcp", addr)
		if err := httpServer.Start(addr); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	} else {
		log.Println("Starting server in stdio mode...")
		if err := server.ServeStdio(s); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}
}
