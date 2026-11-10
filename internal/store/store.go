package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)

var ErrNotFound = errors.New("runner not found")

type Runner struct {
	Name           string    `json:"name"`
	Scope          string    `json:"scope"`
	Labels         []string  `json:"labels"`
	Dir            string    `json:"dir"`
	PauseOnBattery bool      `json:"pause_on_battery"`
	Version        string    `json:"version"`
	Created        time.Time `json:"created"`
}

type Store struct {
	path string
}

func Open(dataDir string) *Store {
	return &Store{path: filepath.Join(dataDir, "runners.json")}
}

func ValidName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid runner name %q: use letters, digits, dot, dash and underscore, up to 63 characters", name)
	}
	return nil
}

func (s *Store) List() ([]Runner, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var runners []Runner
	if err := json.Unmarshal(data, &runners); err != nil {
		return nil, fmt.Errorf("%s is not valid: %w", s.path, err)
	}
	sort.Slice(runners, func(i, j int) bool { return runners[i].Name < runners[j].Name })
	return runners, nil
}

func (s *Store) Get(name string) (Runner, error) {
	runners, err := s.List()
	if err != nil {
		return Runner{}, err
	}
	for _, r := range runners {
		if r.Name == name {
			return r, nil
		}
	}
	return Runner{}, fmt.Errorf("%w: %s", ErrNotFound, name)
}

func (s *Store) Put(r Runner) error {
	runners, err := s.List()
	if err != nil {
		return err
	}
	replaced := false
	for i := range runners {
		if runners[i].Name == r.Name {
			runners[i] = r
			replaced = true
		}
	}
	if !replaced {
		runners = append(runners, r)
	}
	return s.write(runners)
}

func (s *Store) Delete(name string) error {
	runners, err := s.List()
	if err != nil {
		return err
	}
	kept := runners[:0]
	for _, r := range runners {
		if r.Name != name {
			kept = append(kept, r)
		}
	}
	return s.write(kept)
}

func (s *Store) write(runners []Runner) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(runners, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), "runners-*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(append(data, '\n'))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
