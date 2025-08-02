package main

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestRepoFilterWorking tests that repoFilter correctly uses f.repo parameter and filters results
func TestRepoFilterWorking(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}
	ctx := context.Background()

	// Test 1: Get unfiltered results
	t.Log("=== Getting unfiltered results ===")
	unfilteredResult, err := fetchGrepAppPage(ctx, client, map[string]interface{}{"query": "function"}, 1)
	if err != nil {
		t.Fatalf("Unfiltered fetchGrepAppPage failed: %v", err)
	}

	t.Logf("Unfiltered: %d hits, %d total results", len(unfilteredResult.Hits.Hits), unfilteredResult.Facets.Count)

	// Log repositories in unfiltered results
	repoCount := make(map[string]int)
	for _, hit := range unfilteredResult.Hits.Hits {
		repoCount[hit.Repo.Raw]++
	}
	
	t.Log("Repositories in unfiltered results:")
	for repo, count := range repoCount {
		t.Logf("  - %s: %d hits", repo, count)
	}

	// Test 2: Get filtered results for first repository
	var targetRepo string
	for repo := range repoCount {
		targetRepo = repo
		break
	}

	t.Logf("=== Getting filtered results for: %s ===", targetRepo)
	filteredResult, err := fetchGrepAppPage(ctx, client, map[string]interface{}{
		"query":      "function",
		"repoFilter": targetRepo,
	}, 1)
	if err != nil {
		t.Fatalf("Filtered fetchGrepAppPage failed: %v", err)
	}

	t.Logf("Filtered: %d hits, %d total results", len(filteredResult.Hits.Hits), filteredResult.Facets.Count)

	// Verify all results are from target repository
	wrongRepoCount := 0
	for _, hit := range filteredResult.Hits.Hits {
		if hit.Repo.Raw != targetRepo {
			wrongRepoCount++
			t.Errorf("Expected repo %s, got %s", targetRepo, hit.Repo.Raw)
		}
	}

	// Assertions
	if wrongRepoCount == 0 && len(filteredResult.Hits.Hits) > 0 {
		t.Logf("✅ SUCCESS: All %d results are from %s", len(filteredResult.Hits.Hits), targetRepo)
	}

	if filteredResult.Facets.Count < unfilteredResult.Facets.Count {
		t.Logf("✅ SUCCESS: Filtered total (%d) < unfiltered total (%d)", 
			filteredResult.Facets.Count, unfilteredResult.Facets.Count)
	} else {
		t.Errorf("❌ FAIL: Filtered total (%d) should be less than unfiltered total (%d)", 
			filteredResult.Facets.Count, unfilteredResult.Facets.Count)
	}
}

// TestRepoFilterNonExistent tests filtering with a repository that doesn't exist
func TestRepoFilterNonExistent(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}
	ctx := context.Background()

	result, err := fetchGrepAppPage(ctx, client, map[string]interface{}{
		"query":      "function",
		"repoFilter": "nonexistent/fake-repo-12345",
	}, 1)
	if err != nil {
		t.Fatalf("fetchGrepAppPage failed: %v", err)
	}

	t.Logf("Results for non-existent repo: %d hits, %d total", len(result.Hits.Hits), result.Facets.Count)

	if len(result.Hits.Hits) == 0 && result.Facets.Count == 0 {
		t.Log("✅ SUCCESS: Non-existent repository correctly returns 0 results")
	} else {
		t.Errorf("❌ FAIL: Non-existent repository should return 0 results, got %d hits", len(result.Hits.Hits))
	}
}