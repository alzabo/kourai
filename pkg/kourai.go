package kourai

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/agnivade/levenshtein"
	"github.com/ryanbradynd05/go-tmdb"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

var (
	excludedExpr = regexp.MustCompile(`(?i)sample`)
	episodeExpr  = regexp.MustCompile(`(?i)(s\d+e\d+)`)
	sentinelExpr = regexp.MustCompile(`(?i)(\d{3,4}[ip]|s\d{2}|limited|unrated)`)
	seasonExpr   = regexp.MustCompile(`(?i)s(\d+)`)
	dateExpr     = regexp.MustCompile(`(\b(?:19|20)\d{2}\b(?:-\d{1,2}-\d{1,2})?)`)
	title        = cases.Title(language.AmericanEnglish, cases.NoLower)
)

const (
	Movie   = iota
	Unknown = iota
	TV      = iota
)

type Excludes struct {
	Types    map[int]bool
	Patterns []string
}

func (e *Excludes) Type(t int) (ok bool) {
	_, ok = e.Types[t]
	return
}

func NewExcludes(p []string, movies bool, tv bool) Excludes {
	e := Excludes{}
	e.Patterns = p

	e.Types = map[int]bool{}
	if movies {
		e.Types[Movie] = true
	}
	if tv {
		e.Types[TV] = true
	}
	return e
}

type Episode struct {
	ID      string
	Title   string
	Season  int
	Episode int
}

type Media struct {
	Path      string
	Type      int
	Date      string
	Title     string
	TvEpisode Episode
}

func (m *Media) TMDBLookup(c *tmdb.TMDb) {
	opts := map[string]string{}
	if m.Type == TV {
		// nothing is done with this right now
		//c.SearchTv(m.Title, opts)
		res, err := c.SearchTv(m.Title, opts)
		_ = res
		if err != nil {
			fmt.Println("error looking up tv series with title", m.Title, err)
		}
	} else {
		res, err := c.SearchMovie(m.Title, opts)
		if err != nil {
			fmt.Println("error looking up movie with title", m.Title, err)
			return
		}
		title, err := MatchMovieSearch(*m, res)
		if err != nil {
			fmt.Println(err)
			return
		}
		m.Title = title.Title
	}
}

func (m Media) String() string {
	return fmt.Sprintf("Path %v; Type %v; Date %v; ParsedTitle %v; Title %v; TvEpisode %v", m.Path, m.Type, m.Date, m.Title, m.Title, m.TvEpisode)
}

func (m Media) target(d string) string {
	_, file := filepath.Split(m.Path)
	path := ""
	if m.Type != TV {
		if m.Date != "" {
			path = fmt.Sprintf("movies/%s (%s)/%s", m.Title, m.Date, file)
		} else {
			path = fmt.Sprintf("movies/%s/%s", m.Title, file)
		}
	}
	if m.Type == TV {
		season := ""
		if m.TvEpisode.Season == 0 {
			season = "Specials"
		}
		if m.TvEpisode.Season > 0 {
			season = fmt.Sprintf("Season %d", m.TvEpisode.Season)
		}
		if season == "" {
			fmt.Println("failed to create tv path")
			return ""
		}

		path = fmt.Sprintf("tv/%s/%s/%s", m.Title, season, file)
	}

	return filepath.Join(d, path)
}

func MatchMovieSearch(m Media, res *tmdb.MovieSearchResults) (tmdb.MovieShort, error) {
	switch len(res.Results) {
	case 0:
		return tmdb.MovieShort{}, fmt.Errorf("no results found in search for %v", m)
	case 1:
		return res.Results[0], nil
	default:
		match := []tmdb.MovieShort{}
		for _, movie := range res.Results {
			if DateMatch(m.Date, movie.ReleaseDate) {
				match = append(match, movie)
			}
		}
		switch len(match) {
		case 0:
			return tmdb.MovieShort{}, fmt.Errorf("failed to match %v in results %v", m, res)
		case 1:
			return match[0], nil
		default:
			dists := sort.IntSlice{}
			distMap := map[int]tmdb.MovieShort{}
			for _, i := range match {
				d := levenshtein.ComputeDistance(m.Title, i.Title)
				dists = append(dists, d)
				distMap[d] = i
			}
			dists.Sort()
			return distMap[dists[0]], nil
		}
	}
}

func DateMatch(d1, d2 string) bool {
	if d1 == d2 {
		return true
	}

	y1, _, _ := strings.Cut(d1, "-")
	y2, _, _ := strings.Cut(d2, "-")
	return y1 == y2
}

type Link struct {
	Src    string
	Target string
}

func (l Link) Exists() bool {
	_, err := os.Stat(l.Target)
	return !os.IsNotExist(err)
}

func (l Link) Create() {
	if l.Exists() {
		fmt.Printf("target %v already exists\n", l.Target)
		return
	}

	if err := os.MkdirAll(filepath.Dir(l.Target), 0755); err != nil {
		fmt.Printf("error %v encountered when creating path for %v\n", err, l.Target)
		return
	}

	if err := os.Link(l.Src, l.Target); err != nil {
		fmt.Printf("error %v encountered when creating link for %v\n", err, l)
	}
}

func NewMedia(p string) Media {
	m := Media{}
	m.Path = p

	d, f := filepath.Split(p)
	d = filepath.Base(d)
	for _, name := range []string{f, d} {
		end := len(name)
		epLoc := episodeExpr.FindStringIndex(name)
		if epLoc != nil {
			m.Type = TV
			m.TvEpisode = Episode{}
			m.TvEpisode.ID = name[epLoc[0]:epLoc[1]]
			{
				season, err := strconv.Atoi(seasonExpr.FindString(m.TvEpisode.ID)[1:])
				if err != nil {
					fmt.Println("error parsing season number", err)
					m.TvEpisode.Season = -1
				} else {
					m.TvEpisode.Season = season
				}
			}
			{
				_, ep, ok := strings.Cut(strings.ToLower(m.TvEpisode.ID), "e")
				if ok {
					episode, err := strconv.Atoi(ep)
					if err != nil {
						fmt.Print("error parsing episode number", err)
						m.TvEpisode.Episode = -1
					} else {
						m.TvEpisode.Episode = episode
					}
				}
			}
			if epLoc[0] > 0 {
				end = epLoc[0] - 1
			}
		}
		dateLoc := dateExpr.FindStringIndex(name)
		if dateLoc != nil {
			m.Date = name[dateLoc[0]:dateLoc[1]]
			if dateLoc[0] > 0 && dateLoc[0] < end {
				end = dateLoc[0] - 1
			}
		}
		sLoc := sentinelExpr.FindStringIndex(name)
		if sLoc != nil && sLoc[0] > 0 && sLoc[0] < end {
			end = sLoc[0] - 1
		}
		parsed := name[:end]
		if dateLoc != nil || epLoc != nil {
			m.Title = title.String(strings.TrimRight(strings.ReplaceAll(parsed, ".", " "), " -_"))
		}
	}

	return m
}

func FindFiles(f string, exts []string, excludes []string) ([]Media, error) {
	//_, _ = os.Readpath(d)
	media := []Media{}
	excludeExpr := []*regexp.Regexp{}
	for _, p := range excludes {
		excludeExpr = append(excludeExpr, regexp.MustCompile(p))
	}
	if _, err := os.Stat(f); err != nil {
		return media, err
	}
	err := filepath.WalkDir(f, func(path string, d fs.DirEntry, err error) error {
		for _, e := range excludeExpr {
			if e.MatchString(path) {
				return nil
			}
		}

		if d.IsDir() {
			return nil
		}

		if !selected(d.Name(), exts) {
			return nil
		}

		m := NewMedia(path)
		if m.Title != "" {
			media = append(media, m)
			//fmt.Println(m.Type, m.ParsedTitle, m.Date)
		}
		return nil
	})
	return media, err
}

func LinkFromFiles(f []string, exts []string, excludes Excludes, dest string, key string) ([]Link, error) {
	links := []Link{}
	tmdbClient := tmdb.Init(tmdb.Config{APIKey: key})

	for _, i := range f {
		media, err := FindFiles(i, exts, excludes.Patterns)
		if err != nil {
			fmt.Println(err)
			continue
		}

		for _, m := range media {
			if excludes.Type(m.Type) {
				continue
			}
			if key != "" {
				m.TMDBLookup(tmdbClient)
			}
			target := m.target(dest)
			links = append(links, Link{Src: m.Path, Target: target})
		}
	}
	return links, nil
}

func selected(f string, exts []string) bool {
	return selectedExt(f, exts) && !exclude(f)
}

func exclude(f string) bool {
	return excludedExpr.MatchString(f)
}

func selectedExt(f string, exts []string) bool {
	ext := strings.TrimPrefix(filepath.Ext(f), ".")

	sort.Strings(exts)
	i := sort.SearchStrings(exts, ext)
	if i == len(exts) {
		return false
	}
	return exts[i] == ext
}

func Nlinks(d fs.DirEntry) (count uint64, err error) {
	var info fs.FileInfo
	info, err = d.Info()
	if err != nil {
		return
	}
	if sys := info.Sys(); sys != nil {
		if stat, ok := sys.(*syscall.Stat_t); ok {
			count = uint64(stat.Nlink)
		}
	}
	if count == 0 {
		err = fmt.Errorf("failed to determine number of links for file '%s'", d.Name())
	}
	return
}

func Search(f string, key string) {
	client := tmdb.Init(tmdb.Config{APIKey: key})
	res, err := client.SearchTv(f, map[string]string{})
	if err != nil {
		// log
		fmt.Print(err)
		return
	}
	for _, i := range res.Results {
		fmt.Println(i)
	}

}
