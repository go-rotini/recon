package recon

import (
	"strconv"
	"testing"
	"time"
)

// BenchmarkGet_String_Hit measures the hot-path cost of an
// already-cached string lookup. This is the path application
// configuration libraries optimize hardest — every Get on a typical
// service is a hit.
func BenchmarkGet_String_Hit(b *testing.B) {
	r, _ := New()
	defer func() { _ = r.Close() }()
	_ = r.Set("server.port", "8080")
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = r.Get("server.port")
	}
}

// BenchmarkGet_String_Miss measures the cost of a Get that walks
// the source chain and finds nothing. Establishes the per-call
// overhead the resolver imposes when none of the registered
// sources answer.
func BenchmarkGet_String_Miss(b *testing.B) {
	src1 := NewMapSource("a", map[string]any{"x": "1"})
	src2 := NewMapSource("b", map[string]any{"y": "2"})
	r, _ := New(WithSources(src1, src2))
	defer func() { _ = r.Close() }()
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = r.Get("nonexistent.key")
	}
}

// BenchmarkBind_TypicalStruct binds a 20-field mixed-type struct.
// Mirrors the §8.9 benchmark that's published in the README.
func BenchmarkBind_TypicalStruct(b *testing.B) {
	type cfg struct {
		Port       int           `recon:"port"`
		Host       string        `recon:"host"`
		Debug      bool          `recon:"debug"`
		Rate       float64       `recon:"rate"`
		Timeout    time.Duration `recon:"timeout"`
		Tags       []string      `recon:"tags"`
		Workers    int           `recon:"workers"`
		MaxConn    int           `recon:"max_conn"`
		Region     string        `recon:"region"`
		Tier       string        `recon:"tier"`
		BufferSize int           `recon:"buffer_size"`
		QueueSize  int           `recon:"queue_size"`
		Retries    int           `recon:"retries"`
		MinReplica int           `recon:"min_replica"`
		MaxReplica int           `recon:"max_replica"`
		Locale     string        `recon:"locale"`
		AppName    string        `recon:"app_name"`
		Version    string        `recon:"version"`
		Cluster    string        `recon:"cluster"`
		Env        string        `recon:"env"`
	}
	r, _ := New()
	defer func() { _ = r.Close() }()
	_ = r.Set("port", 8080)
	_ = r.Set("host", "localhost")
	_ = r.Set("debug", true)
	_ = r.Set("rate", 1.5)
	_ = r.Set("timeout", "30s")
	_ = r.Set("tags", "a,b,c,d,e")
	_ = r.Set("workers", 16)
	_ = r.Set("max_conn", 100)
	_ = r.Set("region", "us-west-2")
	_ = r.Set("tier", "frontend")
	_ = r.Set("buffer_size", 4096)
	_ = r.Set("queue_size", 1024)
	_ = r.Set("retries", 3)
	_ = r.Set("min_replica", 2)
	_ = r.Set("max_replica", 8)
	_ = r.Set("locale", "en-US")
	_ = r.Set("app_name", "rotini")
	_ = r.Set("version", "1.0.0")
	_ = r.Set("cluster", "prod-1")
	_ = r.Set("env", "production")

	b.ResetTimer()
	for b.Loop() {
		var c cfg
		_ = r.Bind(&c)
	}
}

// BenchmarkLiveGet measures the [Live.Get] atomic-load hot path.
// This is the single most-called method on a live-config app —
// it should compile down to one atomic.Pointer.Load.
func BenchmarkLiveGet(b *testing.B) {
	type cfg struct {
		Port int `recon:"port"`
	}
	r, _ := New()
	defer func() { _ = r.Close() }()
	_ = r.Set("port", 8080)
	live, err := NewLive[cfg](r)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = live.Close() }()
	b.ResetTimer()
	for b.Loop() {
		_ = live.Get()
	}
}

// BenchmarkSnapshotBuild measures the cost of one full snapshot
// rebuild from N sources. The per-rebuild cost matters on every
// Set / SetDefault / AddSource and on every Reload.
func BenchmarkSnapshotBuild(b *testing.B) {
	sources := make([]Source, 0, 8)
	for i := range 8 {
		data := map[string]any{}
		for j := range 32 {
			data["key."+strconv.Itoa(i)+"."+strconv.Itoa(j)] = j
		}
		sources = append(sources, NewMapSource("s"+strconv.Itoa(i), data))
	}
	r, _ := New(WithSources(sources...))
	defer func() { _ = r.Close() }()
	b.ResetTimer()
	for b.Loop() {
		_ = r.Reload()
	}
}

// BenchmarkPathParse measures [ParsePath] over a corpus of
// realistic keys. The path parser runs on every Get / IsSet —
// keeping its cost low is structural.
func BenchmarkPathParse(b *testing.B) {
	corpus := []string{
		"server.port",
		"db.dsn",
		"server.handlers.api.timeout",
		"a.b.c.d.e.f.g.h",
		"[escaped.key]",
		"prefix.[escaped.middle].suffix",
		"single",
	}
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		_ = ParsePath(corpus[i%len(corpus)])
	}
}

// BenchmarkSet_Cold measures the cost of a write that triggers a
// full snapshot rebuild. Set is the slow side of the reads-vs-
// writes asymmetry — readers go through atomic loads while writers
// pay for the full rebuild.
func BenchmarkSet_Cold(b *testing.B) {
	r, _ := New()
	defer func() { _ = r.Close() }()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		_ = r.Set("k", i)
	}
}

// BenchmarkSnapshotAsMap measures the cost of [Snapshot.AsMap] —
// the hot path for [Save] and [Snapshot.AsMap]-driven validators.
func BenchmarkSnapshotAsMap(b *testing.B) {
	r, _ := New()
	defer func() { _ = r.Close() }()
	for i := range 64 {
		_ = r.Set("k."+strconv.Itoa(i), i)
	}
	snap := r.Snapshot()
	b.ResetTimer()
	for b.Loop() {
		_ = snap.AsMap()
	}
}
