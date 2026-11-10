package store

import (
	"errors"
	"testing"
)

func TestPutGetDelete(t *testing.T) {
	s := Open(t.TempDir())
	if runners, err := s.List(); err != nil || len(runners) != 0 {
		t.Fatalf("empty store: %v %v", runners, err)
	}
	if err := s.Put(Runner{Name: "b", Scope: "o/r"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Runner{Name: "a", Scope: "o"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Runner{Name: "b", Scope: "o/changed"}); err != nil {
		t.Fatal(err)
	}
	runners, _ := s.List()
	if len(runners) != 2 || runners[0].Name != "a" || runners[1].Scope != "o/changed" {
		t.Fatalf("unexpected list: %+v", runners)
	}
	if err := s.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"a", "mac-book.1", "x_y"} {
		if ValidName(ok) != nil {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "-a", "a b", "a/b", "../x"} {
		if ValidName(bad) == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
