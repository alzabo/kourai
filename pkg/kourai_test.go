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
		for m := range media {
			// strip tmpdir prefix off of each path
			got = append(got, m.Path()[len(root)+1:len(m.Path())])
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

func TestMovieFromPath(t *testing.T) {
	tt := []struct {
		path  string
		movie *movie
	}{{
		"/foo/bar/Foobar/Foobar.1999.2160p.WEB-DL.mkv",
		&movie{
			path:  "/foo/bar/Foobar/Foobar.1999.2160p.WEB-DL.mkv",
			title: "Foobar",
			year:  1999,
		},
	}, {
		"/foo/bar/night.of.the.BEAST.2022/idk.mkv",
		&movie{
			path:  "/foo/bar/night.of.the.BEAST.2022/idk.mkv",
			title: "Night Of The BEAST",
			year:  2022,
		},
	}}

	for _, w := range tt {
		g, err := MovieFromPath(w.path)
		if err != nil {
			t.Errorf("failed to create movie from path %s", w.path)
		}
		if diff := cmp.Diff(w.movie, g, cmp.AllowUnexported(movie{})); diff != "" {
			t.Errorf("MovieFromPath() mismatch (-want +got):\n%s", diff)
		}
	}
}

func TestEpisodeFromPath(t *testing.T) {
	tt := []struct {
		path    string
		target  string
		episode *episode
	}{{
		"/foo/bar/clobberin.time/clobberin.time.s01e01.lets.go.mkv",
		"tv/Clobberin Time/Season 1/Clobberin Time - S01E01 - Lets Go.mkv",
		&episode{
			path:    "/foo/bar/clobberin.time/clobberin.time.s01e01.lets.go.mkv",
			series:  "Clobberin Time",
			title:   "Lets Go",
			id:      "s01e01",
			season:  1,
			episode: 1,
		},
	}, {
		"/foo/bar/BEASTMODE (2001) - S00E10E11E12.mkv",
		"tv/BEASTMODE (2001)/Specials/BEASTMODE (2001) - S00E10-E12.mkv",
		&episode{
			path:    "/foo/bar/BEASTMODE (2001) - S00E10E11E12.mkv",
			series:  "BEASTMODE",
			title:   "",
			id:      "S00E10E11E12",
			season:  0,
			episode: 10,
			year:    2001,
		},
	}, {
		"/foo/bar/BEASTMODE (2001-01-01) - S01E01 - The BEAST.mkv",
		"tv/BEASTMODE (2001)/Season 1/BEASTMODE (2001) - S01E01 - The BEAST.mkv",
		&episode{
			path:    "/foo/bar/BEASTMODE (2001-01-01) - S01E01 - The BEAST.mkv",
			series:  "BEASTMODE",
			title:   "The BEAST",
			id:      "S01E01",
			season:  1,
			episode: 1,
			year:    2001,
		},
	}, {
		"/a/s/d/f/Nothing/Nothing - S01.E03.mkv",
		"tv/Nothing/Season 1/Nothing - S01E03.mkv",
		&episode{
			path:    "/a/s/d/f/Nothing/Nothing - S01.E03.mkv",
			series:  "Nothing",
			title:   "",
			id:      "S01E03",
			season:  1,
			episode: 3,
		},
	}}

	for _, w := range tt {
		g, err := EpisodeFromPath(w.path)
		if err != nil {
			t.Errorf("failed to create episode from path %s", w.path)
		}
		if diff := cmp.Diff(w.episode, g, cmp.AllowUnexported(episode{})); diff != "" {
			t.Errorf("MovieFromPath() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(w.target, g.Target()); diff != "" {
			t.Errorf("movie.Target() mismatch (-want +got):\n%s", diff)
		}
	}
}

func TestTitlePermutations(t *testing.T) {

}
