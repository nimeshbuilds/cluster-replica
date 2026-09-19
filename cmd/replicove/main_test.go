package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialFileDoesNotOverwriteAndIsPrivate(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "guest")
	remove, err := writeCredential(name, []byte("fixture"))
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(name)
	if info.Mode().Perm() != 0600 {
		t.Fatal("credential permissions")
	}
	if _, err := writeCredential(name, []byte("overwrite")); err == nil {
		t.Fatal("overwrote existing file")
	}
	remove()
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Fatal("file not removed")
	}
	target := filepath.Join(dir, "original")
	_ = os.WriteFile(target, []byte("keep"), 0600)
	_ = os.Symlink(target, name)
	if _, err := writeCredential(name, []byte("overwrite")); err == nil {
		t.Fatal("followed a symlink")
	}
}
