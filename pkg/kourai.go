package kourai

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
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
	cache   *tmdbCache
	options *Options
)

const (
	Movie   = iota
	Unknown = iota
	TV      = iota
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
	excludeTypes   map[int]bool
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
	o.excludeTypes = map[int]bool{}
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
			o.excludeTypes[Movie] = true
		}
		if tv {
			o.excludeTypes[TV] = true
		}
	}
}

func WithFileModificationFilter(after, before *time.Time) Option {
	return func(o *Options) {
		o.fileFilters = append(o.fileFilters, fileMTimeFilter{after, before})
	}
}

func WithCountryFilter(codes []string) Option {
	f := countryFilter{map[string]bool{}}
	for _, code := range codes {
		f.countries[strings.ToLower(code)] = true
	}
	return func(o *Options) {
		o.mediaFilters = append(o.mediaFilters, f)
	}
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
	Countries []string
	TvEpisode Episode
	tmdbID    int
}

func (m *Media) TMDBLookup(c *tmdb.TMDb) {
	if m.Type == TV {
		lookupTV(c, m)
		lookupEpisode(c, m)
	} else {
		res, err := options.TMDBC2.SearchMovie(m.Title, map[string]string{"year": m.Date})
		if err != nil {
			fmt.Println("failed to look up movie:", err)
			return
		}
		m.Title = res.Title
	}
}

func (m Media) String() string {
	return fmt.Sprintf("Path %v; Type %v; Date %v; ParsedTitle %v; Title %v; TvEpisode %v", m.Path, m.Type, m.Date, m.Title, m.Title, m.TvEpisode)
}

func (m Media) target(d string) string {
	_, file := filepath.Split(m.Path)
	path := ""
	mediaDir := ""
	if m.Date != "" {
		mediaDir = fmt.Sprintf("%s (%s)", m.Title, m.Date)
	} else {
		mediaDir = m.Title
	}

	if m.Type != TV {
		path = fmt.Sprintf("movies/%s/%s", mediaDir, file)
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
			ep = strings.ToUpper(fmt.Sprintf("%se%s-e%s", eps[0], strings.Trim(eps[1], "-"), strings.Trim(eps[len(eps)-1], "-")))
		}

		ext := filepath.Ext(file)

		if m.TvEpisode.Title == "" {
			path = fmt.Sprintf("tv/%s/%s/%s - %s%s", mediaDir, season, m.Title, ep, ext)
		} else {
			path = fmt.Sprintf("tv/%s/%s/%s - %s - %s%s", mediaDir, season, m.Title, ep, m.TvEpisode.Title, ext)
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

	if cached, ok := cache.Load(m.Title); ok {
		m.Title = cached.title
		m.tmdbID = cached.id
		return nil
	}

	res, err := c.SearchTv(m.Title, options)
	if err != nil {
		return fmt.Errorf("failed to look up %v", m)
	}

	switch len(res.Results) {
	case 0:
		return fmt.Errorf("no results found in search for %v", m)
	case 1:
		found := lookupItems{
			res.Results[0].Name,
			res.Results[0].ID,
			res.Results[0].OriginCountry,
		}
		cache.Store(m.Title, found)
		m.Title = found.title
		m.tmdbID = found.id
		return nil
	default:
		names := make([]string, len(res.Results))
		for i, j := range res.Results {
			names[i] = j.Name
		}
		idx := BestMatchIndex(m.Title, names)
		found := lookupItems{
			names[idx],
			res.Results[idx].ID,
			res.Results[idx].OriginCountry,
		}
		cache.Store(m.Title, found)
		m.Title = found.title
		m.tmdbID = found.id
		m.Countries = found.countries
		return nil
	}
}

func lookupEpisode(c *tmdb.TMDb, m *Media) error {
	if m.tmdbID == 0 {
		return fmt.Errorf("could not look up episode; TMDB ID not set for %v", m)
	}
	ep, err := c.GetTvEpisodeInfo(m.tmdbID, m.TvEpisode.Season, m.TvEpisode.Episode, map[string]string{})
	if err != nil {
		return fmt.Errorf("failed to look up episode with error %v", err)
	}
	m.TvEpisode.Title = ep.Name
	return nil
}

// When a match can't be found immediately, the search is retried
// in a loop by removing one word at a time from the end of the title.
// To prevent instances of matching on 1 word out of many, which is
// unlikely to yield an accurate match, the number of times the search
// will be retried with a modified title is limited.
func lookupMovie(c *tmdb.TMDb, m *Media) error {
	opts := map[string]string{}
	title := m.Title
	limit := (strings.Count(m.Title, " ") + 1) / 3

	// TODO
	// /mnt/qbittorrent/qBittorrent/downloads/04 ~ Dirty Pair The Movie (1986) (BDRip 1792x1080p x265 HEVC FLAC, AC-3x2 2.0x3)(Triple Audio)[sxales]/Dirty Pair The Movie (1986) (BDRip 1792x1080p x265 HEVC FLAC, AC-3x2 2.0x3)(Triple Audio)[sxales].mkv   /idk/movies/Captain Tsubasa Movie 04: The great world competition The Junior World Cup (1986)/Dirty Pair The Movie (1986) (BDRip 1792x1080p x265 HEVC FLAC, AC-3x2 2.0x3)(Triple Audio)[sxales].mkv
	for i := 0; i <= limit; i++ {
		if title == "" {
			return fmt.Errorf("failed to look up movie with title %s", m.Title)
		}
		// TODO: debug log
		// fmt.Println("Searching for movie", title)
		res, err := c.SearchMovie(title, opts)
		if err != nil {
			return fmt.Errorf("error looking up movie with title %s", title)
		}
		switch len(res.Results) {
		case 0:
			// pop a word off the end and continue
			t, err := titlePop(title)
			if err != nil {
				return err
			}
			title = t
			continue
		case 1:
			m.Title = res.Results[0].Title
			return nil
		default:
			match := []tmdb.MovieShort{}
			for _, movie := range res.Results {
				if err != nil {
					fmt.Println("failed to retrieve movie releases")
				}
				if movieDateMatch(m.Date, movie.ReleaseDate) {
					match = append(match, movie)
				}
			}
			switch len(match) {
			case 0:
				// pop a word off the end and continue
				t, err := titlePop(title)
				if err != nil {
					return err
				}
				title = t
				continue
			case 1:
				m.Title = match[0].Title
				return nil
			default:
				names := make([]string, len(res.Results))
				for i, j := range res.Results {
					names[i] = j.Title
				}
				m.Title = res.Results[BestMatchIndex(title, names)].Title
				return nil
			}
		}
	}
	return nil
}

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

func DateMatch(d1, d2 string) bool {
	if d1 == d2 {
		return true
	}

	y1, _, _ := strings.Cut(d1, "-")
	y2, _, _ := strings.Cut(d2, "-")
	return y1 == y2
}

// some media has a date that is earlier than what TMDB
// returns because TMDB seems to use the theatrical premier.
// however some media have a limited premier, particularly
// at festivals, that are often used as the release year
// otherwise
func movieDateMatch(parsedDate, date string) bool {
	if parsedDate == date {
		return true
	}

	parsedYearString, _, _ := strings.Cut(parsedDate, "-")
	dateYearString, _, _ := strings.Cut(date, "-")

	parsedYear, err := strconv.Atoi(parsedYearString)
	if err != nil {
		return false
	}

	dateYear, err := strconv.Atoi(dateYearString)
	if err != nil {
		return false
	}

	for i := 0; i < 2; i++ {
		if parsedYear+i == dateYear {
			return true
		}
	}
	return false
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

var (
	episodeExpr  = regexp.MustCompile(`(?i)(s\d+)(e\d+)-?(e\d+)*`)
	sentinelExpr = regexp.MustCompile(`(?i)\b(\d{3,4}[ip]|limited|unrated|web(-dl|rip)|bluray|10bit|pal|re(rip|pack)|dvdrip|a\.k\.a\.?|aka)\b`)
	seasonExpr   = regexp.MustCompile(`(?i)s(\d+)`)
	dateExpr     = regexp.MustCompile(`(\b(?:19|20)\d{2}\b(?:-\d{1,2}-\d{1,2})?)`)
)

// func NewLinks(Option...) []Link
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
	t := strings.ReplaceAll(s, ".", " ")
	t = strings.Trim(strings.ReplaceAll(t, "_", " "), "- ")
	if options.SkipTitleCaser {
		return t
	} else {
		return cases.Title(language.AmericanEnglish, cases.NoLower).String(t)
	}
}

type fileMTimeFilter struct {
	after  *time.Time
	before *time.Time
}

func (f fileMTimeFilter) exclude(info fs.FileInfo) bool {
	var a bool
	var b bool
	if f.after != nil {
		a = !info.ModTime().After(*f.after)
	}
	if f.before != nil {
		b = !info.ModTime().Before(*f.before)
	}
	return a || b
}

type fileExtensionFilter struct {
	extensions map[string]bool
}

// files whose extensions are not contained in the fileExtensionFilter
// are excluded
func (f fileExtensionFilter) exclude(info fs.FileInfo) bool {
	if info.IsDir() {
		return false
	}
	split := strings.Split(info.Name(), ".")
	ext := strings.ToLower(split[len(split)-1])
	_, ok := f.extensions[ext]
	return !ok
}

func newFileExtensionFilter(extensions []string) fileExtensionFilter {
	f := fileExtensionFilter{map[string]bool{}}
	for _, ext := range extensions {
		f.extensions[strings.ToLower(ext)] = true
	}
	return f
}

type RegexpFilter struct {
	excludes []*regexp.Regexp
}

func (f RegexpFilter) exclude(info fs.FileInfo) bool {
	for _, e := range f.excludes {
		if e.MatchString(info.Name()) {
			//fmt.Println("skipping", info.Name(), "which matched", e)
			return true
		}
	}
	return false
}

func NewRegexpFilter(excludes []string) RegexpFilter {
	f := RegexpFilter{}
	for _, p := range excludes {
		f.excludes = append(f.excludes, regexp.MustCompile(p))
	}
	return f
}

type fileFilter interface {
	exclude(fs.FileInfo) bool
}

type countryFilter struct {
	countries map[string]bool
}

func (f countryFilter) exclude(m Media) bool {
	for _, country := range m.Countries {
		if _, ok := f.countries[country]; ok {
			return true
		}
	}
	return false
}

type mediaFilter interface {
	exclude(Media) bool
}

func findFiles(root string, filters ...fileFilter) (<-chan Media, <-chan error) {
	c := make(chan Media)
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
				m := NewMedia(path)
				if m.Title != "" {
					c <- m
				}
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
					if _, ok := options.excludeTypes[m.Type]; ok {
						return
					}
					if options.TMDBClient != nil {
						m.TMDBLookup(options.TMDBClient)
						//fmt.Println("Looked up", m)
					}
					for _, filter := range options.mediaFilters {
						if filter.exclude(m) {
							return
						}
					}
					target := m.target(options.dest)
					linkc <- Link{Src: m.Path, Target: target}
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
