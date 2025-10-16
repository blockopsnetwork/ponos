package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sahilm/fuzzy"
)

type FileSearchResult struct {
	Path  string
	Score int
}

type FileSearcher struct {
	files       []string
	maxResults  int
	rootDir     string
	lastRefresh time.Time
	refreshTTL  time.Duration
}

func NewFileSearcher(rootDir string, maxResults int) *FileSearcher {
	return &FileSearcher{
		rootDir:     rootDir,
		maxResults:  maxResults,
		refreshTTL:  time.Minute * 5, 
	}
}

func (fs *FileSearcher) refreshFiles(ctx context.Context) error {
	if time.Since(fs.lastRefresh) < fs.refreshTTL && len(fs.files) > 0 {
		return nil
	}

	var files []string
	err := filepath.Walk(fs.rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil 
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if strings.HasPrefix(info.Name(), ".") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			name := info.Name()
			if name == "node_modules" || name == "vendor" || name == "target" || 
			   name == "build" || name == "dist" || name == ".git" {
				return filepath.SkipDir
			}
		}

		if !info.IsDir() {
			relPath, err := filepath.Rel(fs.rootDir, path)
			if err == nil {
				files = append(files, relPath)
			}
		}

		return nil
	})

	if err != nil && err != context.Canceled {
		return err
	}

	fs.files = files
	fs.lastRefresh = time.Now()
	return nil
}

func (fs *FileSearcher) Search(ctx context.Context, query string) ([]FileSearchResult, error) {
	if err := fs.refreshFiles(ctx); err != nil {
		return nil, err
	}

	if query == "" {
		return nil, nil
	}

	matches := fuzzy.Find(query, fs.files)
	
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Score > matches[j].Score
	})

	limit := fs.maxResults
	if len(matches) < limit {
		limit = len(matches)
	}

	results := make([]FileSearchResult, limit)
	for i := 0; i < limit; i++ {
		results[i] = FileSearchResult{
			Path:  matches[i].Str,
			Score: matches[i].Score,
		}
	}

	return results, nil
}

func (fs *FileSearcher) GetFileCount() int {
	return len(fs.files)
}
