package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadAndWriteSessionIDsFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "snapshot", "session_ids.txt")
	want := []string{
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333",
	}

	if err := writeSessionIDsFile(path, []string{
		want[0],
		want[1],
		want[1],
		want[2],
	}); err != nil {
		t.Fatalf("writeSessionIDsFile: %v", err)
	}

	got, err := loadSessionIDsFile(path)
	if err != nil {
		t.Fatalf("loadSessionIDsFile: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("session ids = %#v, want %#v", got, want)
	}
}
