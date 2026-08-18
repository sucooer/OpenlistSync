package syncer

import (
	"testing"

	"openlist-sync/internal/client"
)

func TestNormalizeExt(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, []string{}},
		{"blank entries dropped", []string{"", "   ", "\t"}, []string{}},
		{"bare suffix", []string{"mp4"}, []string{"*.mp4"}},
		{"leading dot", []string{".txt"}, []string{"*.txt"}},
		{"multiple suffixes", []string{"mp3", "txt", "json"}, []string{"*.mp3", "*.txt", "*.json"}},
		{"existing glob passthrough", []string{"*.mkv", "*foo*"}, []string{"*.mkv", "*foo*"}},
		{"preserve bracket glob", []string{"c[ab]"}, []string{"c[ab]"}},
		{"trims whitespace", []string{"  mp4  ", "txt"}, []string{"*.mp4", "*.txt"}},
		{"just dot dropped", []string{"."}, []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeExt(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("len: got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("[%d]: got %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestNewFilterAcceptsBareSuffixes(t *testing.T) {
	f := NewFilter([]string{"mp4", "mp3"}, nil, nil)
	// "x.mp4" should pass when the user typed "mp4"
	if !f.MatchLocal("x.mp4") {
		t.Fatal("bare suffix should match by extension")
	}
	// non-matching should still be filtered out
	if f.MatchLocal("x.txt") {
		t.Fatal("txt should not match mp4-only filter")
	}
}

func TestNewFilterPreservesExistingGlobs(t *testing.T) {
	f := NewFilter([]string{"*foo*"}, nil, nil)
	if !f.MatchLocal("myfoobar.txt") {
		t.Fatal("existing glob pattern should still work")
	}
	if f.MatchLocal("other.txt") {
		t.Fatal("other.txt should not match *foo*")
	}
}

func TestNewFilterMatchesNestedLocalFileByBasename(t *testing.T) {
	f := NewFilter([]string{"jpg", "json", "nfo"}, nil, nil)
	for _, name := range []string{"album/cover.jpg", "album/info.json", "album/movie.nfo"} {
		if !f.MatchLocal(name) {
			t.Fatalf("nested %q should match extension filter", name)
		}
	}
	if f.MatchLocal("album/movie.mkv") {
		t.Fatal("nested non-matching extension should be filtered out")
	}
}

func TestNewFilterTypeIntegrationStillWorks(t *testing.T) {
	f := NewFilter([]string{"mp4"}, nil, []string{"video"})
	if !f.Match(client.FsObject{Name: "movie.mp4", Type: 2}) {
		t.Fatal("video mp4 should match")
	}
	if f.Match(client.FsObject{Name: "audio.mp3", Type: 3}) {
		t.Fatal("audio mp3 should be filtered out by type")
	}
}
