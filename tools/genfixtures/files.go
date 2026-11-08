package main

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// zipEntryName is where real exports keep the XML inside export.zip.
const zipEntryName = "apple_health_export/export.xml"

// generate writes the XML to disk first (streaming), then zips that file.
// Neither step ever holds the whole dataset in memory.
func generate(dir string, prof profile, seed int64, flat bool) error {
	xmlDir := filepath.Join(dir, "apple_health_export")
	if flat {
		xmlDir = dir
	}
	if err := os.MkdirAll(xmlDir, 0o755); err != nil {
		return err
	}
	xmlPath := filepath.Join(xmlDir, "export.xml")

	f, err := os.Create(xmlPath)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	g := newGen(bw, prof, seed)
	g.writeAll()
	// bufio.Writer remembers the first write error, so one check at the end is enough.
	if err := bw.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return zipFile(xmlPath, filepath.Join(dir, "export.zip"))
}

func zipFile(xmlPath, zipPath string) error {
	in, err := os.Open(xmlPath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(out)
	w, err := zw.Create(zipEntryName)
	if err != nil {
		out.Close()
		return err
	}
	if _, err := io.Copy(w, in); err != nil {
		out.Close()
		return fmt.Errorf("zip copy: %w", err)
	}
	if err := zw.Close(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
