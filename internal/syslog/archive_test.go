package syslog

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeGzip(t *testing.T, path, content string) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestArchivePath(t *testing.T) {
	day := time.Date(2026, 10, 5, 0, 0, 0, 0, cst)
	got := ArchivePath("/opt/asuslog", day)
	if filepath.ToSlash(got) != "/opt/asuslog/merlin-syslog-2026-10-05.log.gz" {
		t.Fatalf("path = %s", got)
	}
}

func TestReadArchiveKeepsOnlyThatDay(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.log.gz")
	writeGzip(t, p, "Oct  4 23:59:59 kernel: previous day\n"+
		"Oct  5 00:00:01 dnsmasq[1]: first\n"+
		"Oct  5 23:59:59 kernel: last\n"+
		"Oct  6 00:00:00 kernel: next day\n")
	lines, err := ReadArchive(p, time.Date(2026, 10, 5, 0, 0, 0, 0, cst))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || !strings.Contains(lines[0].Raw, "first") || !strings.Contains(lines[1].Raw, "last") {
		t.Fatalf("lines = %+v", lines)
	}
	if lines[0].Process != "dnsmasq" {
		t.Errorf("process = %q", lines[0].Process)
	}
}

func TestReadArchiveInfersYearFromDate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.log.gz")
	writeGzip(t, p, "Oct  5 10:00:00 kernel: a year ago\n")
	lines, err := ReadArchive(p, time.Date(2025, 10, 5, 0, 0, 0, 0, cst))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Time.Year() != 2025 {
		t.Fatalf("lines = %+v", lines)
	}
}

func TestReadArchiveMissing(t *testing.T) {
	_, err := ReadArchive(filepath.Join(t.TempDir(), "none.log.gz"), time.Date(2026, 10, 5, 0, 0, 0, 0, cst))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}

func TestReadArchiveRejectsNonGzip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.log.gz")
	if err := os.WriteFile(p, []byte("Oct  5 10:00:00 kernel: plain text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadArchive(p, time.Date(2026, 10, 5, 0, 0, 0, 0, cst)); err == nil {
		t.Fatal("非 gzip 文件应报错")
	}
}

func TestReadArchiveRejectsOversized(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.log.gz")
	writeGzip(t, p, strings.Repeat("Oct  5 10:00:00 kernel: padding padding padding\n", maxArchiveBytes/40))
	if _, err := ReadArchive(p, time.Date(2026, 10, 5, 0, 0, 0, 0, cst)); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("err = %v", err)
	}
}

func TestOnDay(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, cst)
	lines := Parse("Oct  5 23:00:00 kernel: yesterday\nOct  6 01:00:00 kernel: today\ncontinuation\n", now)
	got := OnDay(lines, time.Date(2026, 10, 6, 0, 0, 0, 0, cst))
	if len(got) != 2 || !strings.Contains(got[0].Raw, "today") || got[1].Raw != "continuation" {
		t.Fatalf("got = %+v", got)
	}
}
