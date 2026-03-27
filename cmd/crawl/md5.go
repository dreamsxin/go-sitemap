package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"sync"
)

// MD5Store stores MD5 hashes of crawled page contents.
// It provides thread-safe access to MD5 values for change detection.
type MD5Store struct {
	mu       sync.RWMutex
	hashes   map[string]string // URL string -> MD5 hash
	filePath string            // Path to MD5 storage file
}

// MD5File represents the JSON structure of the MD5 storage file.
type MD5File struct {
	URLs map[string]string `json:"urls"` // Map of URL to its content MD5 hash
}

// NewMD5Store creates a new MD5Store and loads existing hashes from file if present.
//
// Parameters:
//   - filePath: Path to the MD5 storage file (e.g., "sitemap.md5")
//
// Returns:
//   - *MD5Store: Initialized store with loaded hashes
//   - error: Error if file exists but cannot be read/parsed
func NewMD5Store(filePath string) (*MD5Store, error) {
	store := &MD5Store{
		hashes:   make(map[string]string),
		filePath: filePath,
	}

	// Try to load existing hashes from file
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist yet, that's OK
			return store, nil
		}
		return nil, err
	}
	defer file.Close()

	var md5File MD5File
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&md5File); err != nil {
		return nil, err
	}

	store.hashes = md5File.URLs
	return store, nil
}

// GetMD5 retrieves the stored MD5 hash for a URL.
//
// Parameters:
//   - url: URL string to look up
//
// Returns:
//   - string: MD5 hash if found, empty string if not stored
//   - bool: true if hash exists, false otherwise
func (s *MD5Store) GetMD5(url string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	hash, ok := s.hashes[url]
	return hash, ok
}

// SetMD5 stores or updates the MD5 hash for a URL.
//
// Parameters:
//   - url: URL string
//   - hash: MD5 hash of the page content
func (s *MD5Store) SetMD5(url string, hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hashes[url] = hash
}

// HasChanged checks if the MD5 hash for a URL has changed.
//
// Parameters:
//   - url: URL string to check
//   - newHash: New MD5 hash to compare against
//
// Returns:
//   - bool: true if hash changed or URL is new, false if unchanged
func (s *MD5Store) HasChanged(url string, newHash string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	oldHash, exists := s.hashes[url]
	if !exists {
		return true // New URL, consider as changed
	}
	return oldHash != newHash
}

// Save writes all MD5 hashes to the storage file.
//
// Returns:
//   - error: Error if file cannot be written
func (s *MD5Store) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	md5File := MD5File{
		URLs: s.hashes,
	}

	file, err := os.Create(s.filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(md5File)
}

// CalculateMD5 computes the MD5 hash of the provided content.
//
// Parameters:
//   - content: Byte slice of page content
//
// Returns:
//   - string: Hexadecimal MD5 hash string
func CalculateMD5(content []byte) string {
	hash := md5.Sum(content)
	return hex.EncodeToString(hash[:])
}

// CalculateMD5FromReader computes the MD5 hash by reading from an io.Reader.
// This is useful for streaming content without loading it all into memory.
//
// Parameters:
//   - reader: Reader to read content from
//
// Returns:
//   - string: Hexadecimal MD5 hash string
//   - error: Error if reading fails
func CalculateMD5FromReader(reader io.Reader) (string, error) {
	hasher := md5.New()
	if _, err := io.Copy(hasher, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
