package importer

import (
	"os"
	"testing"
)

// BenchmarkImport imports a big file you generate yourself (it is never
// committed). Skipped unless HEALTH_BENCH_FILE points at an export.xml or zip:
//
//	go run ./tools/genfixtures --size large --out "$TMPDIR/large"
//	HEALTH_BENCH_FILE=$TMPDIR/large/apple_health_export/export.xml \
//	  go test ./internal/importer -run xxx -bench Import -benchtime 1x
func BenchmarkImport(b *testing.B) {
	file := os.Getenv("HEALTH_BENCH_FILE")
	if file == "" {
		b.Skip("set HEALTH_BENCH_FILE to run")
	}
	var rows int64
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		conn := newDB(b) // fresh empty database each round
		b.StartTimer()
		sum, err := Import(conn, file, Options{})
		if err != nil {
			b.Fatal(err)
		}
		rows += sum.Read
	}
	b.ReportMetric(float64(rows)/b.Elapsed().Seconds(), "records/s")
}
