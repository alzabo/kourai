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
	episodeExpr  = regexp.MustCompile(`(?i)(s\d+)(e\d+)-?(e\d+)*`)
	sentinelExpr = regexp.MustCompile(`(?i)\b(\d{3,4}[ip]|limited|unrated|web(-dl|rip)|bluray|10bit|pal|re(rip|pack)|dvdrip)\b`)
	seasonExpr   = regexp.MustCompile(`(?i)s(\d+)`)
	dateExpr     = regexp.MustCompile(`(\b(?:19|20)\d{2}\b(?:-\d{1,2}-\d{1,2})?)`)
	title        = cases.Title(language.AmericanEnglish, cases.NoLower)
	cache        = make(map[string]string)
	options      = Options{}
)

const (
	Movie   = iota
	Unknown = iota
	TV      = iota
)

type Options struct {
	SkipTitleCaser bool
	APIKey         string
}

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
	tmdbID    int
}

func (m *Media) TMDBLookup(c *tmdb.TMDb) {
	opts := map[string]string{}
	if m.Type == TV {
		lookupTV(c, m)
	} else {
		res, err := c.SearchMovie(m.Title, opts)
		if err != nil {
			fmt.Println("error looking up movie with title", m.Title, err)
			return
		}
		title, err := MatchMovieSearch(m, res)
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

		ep := strings.ToUpper(m.TvEpisode.ID)
		// format episodes the way plex likes, including episode IDs
		eps := strings.Split(strings.ToLower(m.TvEpisode.ID), "e")
		if len(eps) > 2 {
			ep = strings.ToUpper(fmt.Sprintf("%se%s-e%s", eps[0], eps[1], eps[len(eps)-1]))
		}

		ext := filepath.Ext(file)

		if m.TvEpisode.Title == "" {
			path = fmt.Sprintf("tv/%s/%s/%s - %s%s", m.Title, season, m.Title, ep, ext)
		} else {
			path = fmt.Sprintf("tv/%s/%s/%s - %s - %s%s", m.Title, season, m.Title, ep, m.TvEpisode.Title, ext)
		}
	}

	return filepath.Join(d, path)
}

func MatchMovieSearch(m *Media, res *tmdb.MovieSearchResults) (tmdb.MovieShort, error) {
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
			names := make([]string, len(res.Results))
			for i, j := range res.Results {
				names[i] = j.Title
			}
			return res.Results[BestMatchIndex(m.Title, names)], nil
		}
	}
}

func lookupTV(c *tmdb.TMDb, m *Media) error {
	options := map[string]string{}

	if title, ok := cache[m.Title]; ok {
		m.Title = title
		//fmt.Println("cache hit for", m)
		return nil
	} else {
		res, err := c.SearchTv(m.Title, options)
		if err != nil {
			return fmt.Errorf("failed to look up %v", m)
		}

		switch len(res.Results) {
		case 0:
			return fmt.Errorf("no results found in search for %v", m)
		case 1:
			cache[m.Title] = res.Results[0].Name
			m.Title = cache[m.Title]
			m.tmdbID = res.Results[0].ID
			return nil
		default:
			names := make([]string, len(res.Results))
			for i, j := range res.Results {
				names[i] = j.Name
			}
			i := BestMatchIndex(m.Title, names)
			cache[m.Title] = names[i]
			m.Title = names[i]
			m.tmdbID = res.Results[i].ID
			return nil
		}
	}
}

func BestMatchIndex(s string, c []string) int {
	dists := sort.IntSlice{}
	dMap := map[int]int{}
	for i, j := range c {
		d := levenshtein.ComputeDistance(s, j)
		dists = append(dists, d)
		dMap[d] = i
	}
	dists.Sort()
	return dMap[dists[0]]
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
	m.TvEpisode = Episode{
		Season:  -1,
		Episode: -1,
	}

	d, f := filepath.Split(p)
	d = filepath.Base(d)
	for _, name := range []string{f, d} {
		end := len(name)
		epLoc := episodeExpr.FindStringIndex(name)

		if epLoc != nil {
			if epLoc[0] > 0 {
				end = epLoc[0] - 1
			}

			m.Type = TV
			m.TvEpisode.ID = name[epLoc[0]:epLoc[1]]
			{
				season, err := strconv.Atoi(seasonExpr.FindString(m.TvEpisode.ID)[1:])
				if err != nil {
					fmt.Println("error parsing season number", err)
				} else {
					m.TvEpisode.Season = season
				}
			}
			{
				eps := strings.Split(strings.ToLower(m.TvEpisode.ID), "e")
				episode, err := strconv.Atoi(strings.TrimSuffix(eps[1], "-"))

				if err != nil {
					fmt.Println("error parsing episode number from", m.TvEpisode.ID, "with error", err)
				} else {
					m.TvEpisode.Episode = episode
				}
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
		if dateLoc != nil || epLoc != nil {
			n := name[:end]
			m.Title = makeTitle(n)
		}
		if epLoc != nil {
			start := epLoc[1] + 1
			end := len(name)
			if dateLoc != nil && dateLoc[0] < end && dateLoc[0]-1 > start {
				end = dateLoc[0] - 1
			}
			//if sLoc != nil {
			//	fmt.Println(name, "start:", start, "end:", end, "sentinel:", name[sLoc[0]:sLoc[1]], "sLog[0]:", sLoc[0], "episode", epLoc[1])
			//}
			if sLoc != nil && sLoc[0] < end {
				if sLoc[0] > start {
					end = sLoc[0] - 1
				}
				// If the slice start is the same as the sentinel match, the
				// title is probably not in the name
				if sLoc[0] == start {
					end = start
				}
			}
			n := name[start:end]
			m.TvEpisode.Title = makeTitle(n)
		}
	}

	return m
}

// Normalize a title
func makeTitle(s string) string {
	t := strings.Trim(strings.ReplaceAll(s, ".", " "), "_- ")
	if options.SkipTitleCaser {
		return t
	} else {
		return title.String(t)
	}
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

func LinkFromFiles(f []string, exts []string, excludes Excludes, dest string, opts Options) ([]Link, error) {
	links := []Link{}
	options = opts
	tmdbClient := tmdb.Init(tmdb.Config{APIKey: options.APIKey})

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
			if options.APIKey != "" {
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
