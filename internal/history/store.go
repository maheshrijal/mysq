package history

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maheshrijal/mysqldot/internal/model"
)

type Store struct{ Root string }

type Item struct {
	Fingerprint string    `json:"fingerprint"`
	Database    string    `json:"database"`
	Host        string    `json:"host"`
	Count       int       `json:"count"`
	Oldest      time.Time `json:"oldest"`
	Newest      time.Time `json:"newest"`
}

func Open(path string) (*Store, error) {
	if path == "" {
		var err error
		path, err = defaultRoot()
		if err != nil {
			return nil, err
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve history path: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("create history store: %w", err)
	}
	return &Store{Root: abs}, nil
}

func defaultRoot() (string, error) {
	if root := os.Getenv("XDG_STATE_HOME"); root != "" {
		return filepath.Join(root, "mysqldot", "snapshots"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "mysqldot", "snapshots"), nil
}

func (s *Store) Save(ctx *model.Context) (string, error) {
	if ctx.Fingerprint == "" {
		return "", errors.New("cannot store a snapshot without a fingerprint")
	}
	directory := filepath.Join(s.Root, ctx.Fingerprint)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%020d.json.gz", ctx.CollectedAt.UnixNano())
	path := filepath.Join(directory, name)
	temp, err := os.CreateTemp(directory, ".snapshot-*.tmp")
	if err != nil {
		return "", err
	}
	tempName := temp.Name()
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = os.Remove(tempName)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return "", err
	}
	gz := gzip.NewWriter(temp)
	encoder := json.NewEncoder(gz)
	if err := encoder.Encode(ctx); err != nil {
		_ = gz.Close()
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	if err := temp.Sync(); err != nil {
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tempName, path); err != nil {
		return "", err
	}
	committed = true
	return path, nil
}

func (s *Store) Latest(fingerprint string) (*model.Context, error) {
	paths, err := s.paths(fingerprint)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, nil
	}
	return load(paths[len(paths)-1])
}

func (s *Store) Pair(fingerprint string, since time.Duration) (*model.Context, *model.Context, error) {
	paths, err := s.paths(fingerprint)
	if err != nil {
		return nil, nil, err
	}
	if len(paths) < 2 {
		return nil, nil, nil
	}
	current, err := load(paths[len(paths)-1])
	if err != nil {
		return nil, nil, err
	}
	cutoff := current.CollectedAt.Add(-since)
	for i := len(paths) - 2; i >= 0; i-- {
		candidate, err := load(paths[i])
		if err != nil {
			return nil, nil, err
		}
		if !candidate.CollectedAt.After(cutoff) {
			return candidate, current, nil
		}
	}
	return nil, current, nil
}

func (s *Store) List() ([]Item, error) {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0)
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		paths, err := s.paths(entry.Name())
		if err != nil || len(paths) == 0 {
			continue
		}
		oldest, err := load(paths[0])
		if err != nil {
			continue
		}
		newest, err := load(paths[len(paths)-1])
		if err != nil {
			continue
		}
		items = append(items, Item{Fingerprint: entry.Name(), Database: newest.Server.Database,
			Host: fmt.Sprintf("%s:%d", newest.Server.Host, newest.Server.Port), Count: len(paths),
			Oldest: oldest.CollectedAt, Newest: newest.CollectedAt})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Newest.After(items[j].Newest) })
	return items, nil
}

func (s *Store) paths(fingerprint string) ([]string, error) {
	if fingerprint == "" || strings.ContainsAny(fingerprint, `/\\`) {
		return nil, errors.New("invalid snapshot fingerprint")
	}
	paths, err := filepath.Glob(filepath.Join(s.Root, fingerprint, "*.json.gz"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func load(path string) (*model.Context, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	decoder := json.NewDecoder(io.LimitReader(gz, 128<<20))
	var ctx model.Context
	if err := decoder.Decode(&ctx); err != nil {
		return nil, err
	}
	return &ctx, nil
}
