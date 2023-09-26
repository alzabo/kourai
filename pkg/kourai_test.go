package kourai

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestFindFiles(t *testing.T) {
	tt := []struct {
		files    []string
		found    []string
		excludes []string
	}{
		{
			[]string{"foobar/no-match.mkv"},
			[]string{},
			nil,
		},
		{
			[]string{
				"xyzzy/42/Night of the Foo Bar (1968)/dir-match.mkv",
				"file-match/The Day of The Baz (1979).mkv",
				"Clobberin' Time/Season 1/Clobberin' Time S01E02.avi",
			},
			[]string{
				"xyzzy/42/Night of the Foo Bar (1968)/dir-match.mkv",
				"file-match/The Day of The Baz (1979).mkv",
				"Clobberin' Time/Season 1/Clobberin' Time S01E02.avi",
			},
			nil,
		},
		{
			[]string{
				"included/A (1999).mkv",
				"included/B (2001).mkv",
				"excluded-dir/nested/01A9 (2005).mkv",
				"excluded-dir/nested/BE0F (2008).mkv",
			},
			[]string{
				"included/A (1999).mkv",
				"included/B (2001).mkv",
			},
			[]string{`^excluded-dir$`},
		},
	}

	for _, i := range tt {
		i := i
		root := t.TempDir()
		for _, file := range i.files {
			os.MkdirAll(filepath.Join(root, filepath.Dir(file)), 0755)
			f, err := os.Create(filepath.Join(root, file))
			if err != nil {
				t.Error("failed to create test file", file)
			}
			defer f.Close()
		}

		// The returned slice of Media items is sorted according to
		// the order in which the file was visited.
		got := sort.StringSlice{}
		media, _ := findFiles(root, NewRegexpFilter(i.excludes))
		for _, m := range media {
			// strip tmpdir prefix off of each path
			m := m
			got = append(got, m.Path[len(root)+1:len(m.Path)])
		}
		got.Sort()
		want := sort.StringSlice(i.found)
		want.Sort()

		//t.Log("want:", want)
		//t.Log("got:", got)

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("findFiles() mismatch (-want +got):\n%s", diff)
		}
	}
}
