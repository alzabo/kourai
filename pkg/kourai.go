package kourai

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	tmdb_ "github.com/alzabo/kourai/tmdb"

	"github.com/agnivade/levenshtein"
	"github.com/ryanbradynd05/go-tmdb"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// TODO: filtering before works, but results are empty because the mtime on the root folder
// is newer

var (
	cache        *tmdbCache
	options      *Options
	episodeExpr  = regexp.MustCompile(`(?i)(s\d+)(e\d+)-?(e\d+)*`)
	sentinelExpr = regexp.MustCompile(`(?i)\b(\d{3,4}[ip]|limited|unrated|web(-dl|rip)|bluray|10bit|pal|re(rip|pack)|dvdrip|a\.k\.a\.?|aka)\b`)
	seasonExpr   = regexp.MustCompile(`(?i)s(\d+)`)
	dateExpr     = regexp.MustCompile(`(?:\b(19|20)\d{2}\b(?:-\d{1,2}-\d{1,2})?)`)
)

func newTMDBCache() *tmdbCache {
	c := tmdbCache{
		internal: map[string]lookupItems{},
	}
	return &c
}

type tmdbCache struct {
	mutex    sync.RWMutex
	internal map[string]lookupItems
}

func (c *tmdbCache) Store(k string, v lookupItems) {
	c.mutex.Lock()
	c.internal[k] = v
	c.mutex.Unlock()
}

func (c *tmdbCache) Load(k string) (lookupItems, bool) {
	c.mutex.RLock()
	v, ok := c.internal[k]
	c.mutex.RUnlock()
	return v, ok
}

type lookupItems struct {
	title     string
	id        int
	countries []string
}

type Options struct {
	SkipTitleCaser bool
	TMDBClient     *tmdb.TMDb
	TMDBC2         *tmdb_.TMDB
	fileFilters    []fileFilter
	mediaFilters   []mediaFilter
	sources        []string
	dest           string
	excludeTypes   map[string]struct{}
}

func (o *Options) SetOptions(opts ...Option) {
	for _, setOption := range opts {
		setOption(o)
	}
}

func NewOptions() *Options {
	defaultFilter := NewRegexpFilter([]string{`(?i)\bsample\b`})

	o := &Options{}
	o.fileFilters = append(o.fileFilters, defaultFilter)
	o.excludeTypes = map[string]struct{}{}
	return o
}

type Option func(*Options)

func WithFileExtensions(exts []string) Option {
	return func(o *Options) {
		o.fileFilters = append(o.fileFilters, newFileExtensionFilter(exts))
	}
}

func WithExcludePatterns(patterns []string) Option {
	return func(o *Options) {
		o.fileFilters = append(o.fileFilters, NewRegexpFilter(patterns))
	}
}

func WithTMDBApiKey(k string) Option {
	return func(o *Options) {
		if k == "" {
			return
		}
		o.TMDBClient = tmdb.Init(tmdb.Config{APIKey: k})
		o.TMDBC2 = tmdb_.New(k)
	}
}

func WithoutTitleCaseModification(disabled bool) Option {
	return func(o *Options) {
		o.SkipTitleCaser = disabled
	}
}

func WithDestination(dest string) Option {
	return func(o *Options) {
		o.dest = dest
	}
}

func WithSources(sources []string) Option {
	return func(o *Options) {
		o.sources = sources
	}
}

func WithExcludeTypes(movies bool, tv bool) Option {
	return func(o *Options) {
		if movies {
			o.excludeTypes["movie"] = struct{}{}
		}
		if tv {
			o.excludeTypes["episode"] = struct{}{}
		}
	}
}

func WithFileModificationFilter(after, before *time.Time) Option {
	return func(o *Options) {
		o.fileFilters = append(o.fileFilters, fileMTimeFilter{after, before})
	}
}

// TODO: collect additional metadata when filters that require it are enabled.
func WithCountryFilter(codes []string) Option {
	f := countryFilter{map[string]bool{}}
	for _, code := range codes {
		f.countries[strings.ToLower(code)] = true
	}
	return func(o *Options) {
		o.mediaFilters = append(o.mediaFilters, f)
	}
}

type episode struct {
	series  string
	title   string
	id      string
	season  int
	episode int
	year    int
	path    string
	tmdbID  int
}

func (e *episode) Path() string {
	return e.path
}

func (e *episode) Target() string {
	var season string
	if e.season == 0 {
		season = "Specials"
	} else {
		season = fmt.Sprintf("Season %d", e.season)
	}

	// format episode ID the way plex likes, including episode IDs
	var ep string
	eps := strings.Split(strings.ToLower(e.id), "e")
	// Handle edge case episodes where multiple episodes are combined in a
	// single file, e.g. s01e01e02, rendering it as S01E01-E02
	if len(eps) > 2 {
		s := eps[0]
		first := strings.Trim(eps[1], "-")
		last := strings.Trim(eps[len(eps)-1], "-")
		ep = strings.ToUpper(fmt.Sprintf("%se%s-e%s", s, first, last))
	} else {
		ep = strings.ToUpper(e.id)
	}

	var series string
	if e.year != 0 {
		series = fmt.Sprintf("%s (%d)", e.series, e.year)
	} else {
		series = e.series
	}

	var target string
	dir := fmt.Sprintf("tv/%s/%s", series, season)
	ext := filepath.Ext(e.path)
	if e.title != "" {
		target = fmt.Sprintf("%s/%s - %s - %s%s", dir, series, ep, e.title, ext)
	} else {
		target = fmt.Sprintf("%s/%s - %s%s", dir, series, ep, ext)
	}
	return target
}

func EpisodeFromPath(path string) (*episode, error) {
	ep := &episode{path: path}
	var errs []error
	var title [2]int
	var series [2]int

	_, file := filepath.Split(path)
	ext := filepath.Ext(file)
	basename := file[:len(file)-len(ext)]

	if loc := episodeExpr.FindStringIndex(basename); loc != nil {
		ep.id = basename[loc[0]:loc[1]]
		title[0] = loc[1] + 1
		if loc[0] > 0 {
			series[1] = loc[0] - 1
		}
	} else {
		return ep, fmt.Errorf("could not determine episode ID given path \"%s\"; expression %v", basename, episodeExpr)
	}

	if s, err := strconv.Atoi(seasonExpr.FindString(ep.id)[1:]); err != nil {
		errs = append(errs, fmt.Errorf("error parsing season number: %w", err))
	} else {
		ep.season = s
	}

	eps := strings.Split(strings.ToLower(ep.id), "e")
	if e, err := strconv.Atoi(strings.TrimSuffix(eps[1], "-")); err != nil {
		errs = append(errs, fmt.Errorf("error parsing episode number from %s with error %w", ep.id, err))
	} else {
		ep.episode = e
	}

	title[1] = len(basename)
	if loc := dateExpr.FindStringIndex(basename); loc != nil {
		date := basename[loc[0]:loc[1]]
		year, _, _ := strings.Cut(date, "-")
		var err error
		ep.year, err = strconv.Atoi(year)
		if err != nil {
			errs = append(errs, fmt.Errorf("could not parse episode year with error %w", err))
		}
		// If a date is given, it will come after the series name.
		// The end index of the series is updated to the index before the
		// beginning of the date match
		if loc[0] < series[1] && loc[0]-1 > series[0] {
			series[1] = loc[0] - 1
		}
	}
	if loc := sentinelExpr.FindStringIndex(basename); loc != nil {
		if loc[0] < title[1] {
			if loc[0] > title[0] {
				title[1] = loc[0] - 1
			} else
			// If the title start is the same as the sentinel match, the
			// title is probably not in the name
			if loc[0] == title[0] {
				title[1] = title[0]
			}
		}
	}
	if title[0] <= title[1] {
		n := basename[title[0]:title[1]]
		ep.title = makeTitle(n)
	}
	ep.series = makeTitle(basename[series[0]:series[1]])

	return ep, errors.Join(errs...)
}

type movie struct {
	title  string
	year   string // TODO: int instead of string
	path   string
	tmdbID int
}

func (m *movie) Path() string {
	return m.path
}

func (m *movie) Target() string {
	_, file := filepath.Split(m.path)
	var dir string
	if m.year != "" {
		dir = fmt.Sprintf("%s (%s)", m.title, m.year)
	} else {
		dir = m.title
	}
	return fmt.Sprintf("movies/%s/%s", dir, file)
}

func MovieFromPath(path string) (*movie, error) {
	mov := &movie{path: path}
	var errs []error

	dir, file := filepath.Split(path)
	dir = filepath.Base(dir)
	ext := filepath.Ext(file)
	basename := file[:len(file)-len(ext)]

	for _, i := range [2]string{basename, dir} {
		end := len(i)
		dateLoc := dateExpr.FindStringIndex(i)
		if dateLoc != nil {
			mov.year = i[dateLoc[0]:dateLoc[1]]
			if dateLoc[0] > 0 && dateLoc[0] < end {
				end = dateLoc[0] - 1
			}
		}
		sLoc := sentinelExpr.FindStringIndex(i)
		if sLoc != nil && sLoc[0] > 0 && sLoc[0] < end {
			end = sLoc[0] - 1
		}
		n := i[:end]
		mov.title = makeTitle(n)
		if mov.title != "" && mov.year != "" {
			break
		}
	}
	if mov.title == "" || mov.year == "" {
		errs = append(errs, fmt.Errorf("failed to create movie from path \"%s\"; invalid movie %v", path, *mov))
	}
	return mov, errors.Join(errs...)
}

type Linkable interface {
	Path() string
	Target() string
}

func NewLinkable(path string) (Linkable, error) {
	var l Linkable
	var err error

	if episodeExpr.FindString(path) != "" {
		l, err = EpisodeFromPath(path)
	} else {
		l, err = MovieFromPath(path)
	}
	return l, err
}

func TMDBLookup(l Linkable, c *tmdb.TMDb) {
	switch v := l.(type) {
	case *episode:
		details, err := options.TMDBC2.SearchEpisode(v.series, v.year, v.season, v.episode)
		if err != nil {
			return
		}
		v.title = details.Name
	case *movie:
		res, err := options.TMDBC2.SearchMovie(v.title, map[string]string{"year": v.year})
		if err != nil {
			fmt.Println("failed to look up movie:", err)
			return
		}
		v.title = res.Title
	}
}

// TODO: Port to new movie matcher
func titlePop(t string) (string, error) {
	items := strings.Split(t, " ")
	if len(items) < 1 {
		return "", errors.New("created empty string when removing last word")
	}
	return strings.Join(items[0:len(items)-1], " "), nil
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

type Link struct {
	Src    string
	Target string
}

func (ln Link) Exists() bool {
	_, err := os.Stat(ln.Target)
	return !os.IsNotExist(err)
}

func (ln Link) Create() {
	if ln.Exists() {
		fmt.Printf("target %v already exists\n", ln.Target)
		return
	}

	if err := os.MkdirAll(filepath.Dir(ln.Target), 0755); err != nil {
		fmt.Printf("error %v encountered when creating path for %v\n", err, ln.Target)
		return
	}

	if err := os.Link(ln.Src, ln.Target); err != nil {
		fmt.Printf("error %v encountered when creating link for %v\n", err, ln)
	}
}

func LinkFromMedia(l Linkable, destdir string) Link {
	ln := Link{
		Src:    l.Path(),
		Target: path.Join(destdir, l.Target()),
	}
	return ln
}

// Normalize a title
func makeTitle(s string) string {
	t := strings.ReplaceAll(s, ".", " ")
	t = strings.Trim(strings.ReplaceAll(t, "_", " "), "- ")
	if options.SkipTitleCaser {
		return t
	} else {
		return cases.Title(language.AmericanEnglish, cases.NoLower).String(t)
	}
}

func findFiles(root string, filters ...fileFilter) (<-chan Linkable, <-chan error) {
	c := make(chan Linkable)
	errc := make(chan error, 1)
	if _, err := os.Stat(root); err != nil {
		close(c)
		errc <- fmt.Errorf("failed to stat %s with error %s", root, err)
		return c, errc
	}
	go func() {
		var wg sync.WaitGroup
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			var info fs.FileInfo
			if i, err := d.Info(); err != nil {
				return nil
			} else {
				info = i
			}

			// If a directory matches an exclude filter, return to the
			// WalkDirFunc to skip its children too
			if d.IsDir() {
				for _, filter := range filters {
					if filter.exclude(info) {
						// TODO: debug logging
						//slog.Debug("skipping directory", info)
						//fmt.Println("skipping directory", path, "filter", filter)
						return fs.SkipDir
					}
				}
				return nil
			}

			// Non-directory files can be filtered concurrently
			wg.Add(1)
			go func() {
				defer wg.Done()
				if !d.Type().IsRegular() { // TODO: Handle symlinks?
					return
				}
				for _, filter := range filters {
					if filter.exclude(info) {
						return
					}
				}
				m, err := NewLinkable(path)
				if err != nil {
					return
				}
				c <- m
			}()
			return nil
		})
		go func() {
			wg.Wait()
			close(c)
		}()
		errc <- err
	}()
	return c, errc
}

// TODO: accept done channel
func LinkFromFiles(optionConfig ...Option) (<-chan Link, <-chan error) {
	options.SetOptions(optionConfig...)
	linkc := make(chan Link)
	errc := make(chan error, 1)

	go func() {
		wg := sync.WaitGroup{}

		for _, src := range options.sources {
			media, errc := findFiles(src, options.fileFilters...)
			if err := <-errc; err != nil {
				fmt.Println(err)
				continue
			}
			for m := range media {
				m := m
				wg.Add(1)
				go func() {
					defer wg.Done()
					// type exclusion may be done before an expensive TMDBLookup call
					// because the required properties are already set
					switch m.(type) {
					case *movie:
						if _, ok := options.excludeTypes["movie"]; ok {
							return
						}
					case *episode:
						if _, ok := options.excludeTypes["episode"]; ok {
							return
						}
					}
					if options.TMDBClient != nil {
						TMDBLookup(m, options.TMDBClient)
					}
					for _, filter := range options.mediaFilters {
						if filter.exclude(m) {
							return
						}
					}
					linkc <- LinkFromMedia(m, options.dest)
				}()
			}
		}
		go func() {
			wg.Wait()
			close(linkc)
		}()
	}()
	errc <- nil
	return linkc, errc
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

func Search(key string, f string, options map[string]string) {
	client := tmdb_.New(key)
	res, errc := client.SearchMovies(f, nil, options)
	if err := <-errc; err != nil {
		// log
		fmt.Print(err)
		return
	}
	for r := range res {
		fmt.Printf("%s\t\t%s\n", r.Title, r.Overview)
	}
}

func init() {
	options = NewOptions()
	cache = newTMDBCache()
}
