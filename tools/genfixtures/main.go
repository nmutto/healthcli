// Command genfixtures writes a SYNTHETIC Apple Health export (export.xml and
// export.zip) for testing the importer. Nothing here is real health data: every
// value is invented from the public export.xml format.
//
//	go run ./tools/genfixtures --size small --seed 1 --out testdata/small --flat
//	go run ./tools/genfixtures --size large --out testdata/large
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	size := flag.String("size", "small", "dataset size: small (~14 days) or large (several years, hundreds of MB)")
	seed := flag.Int64("seed", 1, "random seed; the same seed always gives byte-identical output")
	out := flag.String("out", "", "output directory (default testdata/<size>)")
	flat := flag.Bool("flat", false, "write DIR/export.xml instead of DIR/apple_health_export/export.xml")
	flag.Parse()

	prof, ok := profiles[*size]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown --size %q (want small or large)\n", *size)
		os.Exit(2)
	}
	dir := *out
	if dir == "" {
		dir = "testdata/" + *size
	}
	if err := generate(dir, prof, *seed, *flat); err != nil {
		fmt.Fprintln(os.Stderr, "genfixtures:", err)
		os.Exit(1)
	}
}
