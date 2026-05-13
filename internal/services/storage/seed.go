package storage

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func osStat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func walkDir(root string, fn func(relPath, absPath string) error) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		return fn(rel, p)
	})
}

func detectContentType(name string, data []byte) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".json":
		return "application/json"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".csv":
		return "text/csv"
	}
	return http.DetectContentType(data)
}
