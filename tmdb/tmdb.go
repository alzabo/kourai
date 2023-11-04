package kourai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

var (
	limiter  *rate.Limiter
	requestc chan request
)

type request struct {
	url       string
	container any
	errc      chan error
}

type MovieSearchResults struct {
	Results      []MovieSearchResult
	Page         int
	TotalPages   int
	TotalResults int
}

// TODO: genre IDs
type MovieSearchResult struct {
	Title string
	Adult bool
	ID    uint32

	// TODO: some other type here, Custom Unmarshal function
	OriginalLanguage string
	OriginalTitle    string
	Overview         string
	Popularity       float32

	ReleaseDate time.Time
	VoteAverage float32
	VoteCount   uint32
}

func (ms *MovieSearchResult) UnmarshalJSON(b []byte) error {
	type Alias MovieSearchResult
	aux := &struct {
		*Alias
		ReleaseDate string `json:"release_date"`
	}{
		Alias: (*Alias)(ms),
	}
	if err := json.Unmarshal(b, aux); err != nil {
		return err
	}

	t, err := time.Parse("2006-01-02", aux.ReleaseDate)
	if err != nil {
		return nil
	}
	ms.ReleaseDate = t

	return nil
}

type TVSearchResults struct {
	Results      []TVSearchResult
	Page         int
	TotalPages   int
	TotalResults int
}

type TVSearchResult struct {
	Name             string
	Adult            bool
	ID               uint32
	OriginCountry    []string
	OriginalLanguage string
	OriginalName     string
	Overview         string
	FirstAirDate     time.Time
}

type EpisodeDetails struct {
	Name string
	ID   uint32

	SeasonNumber  uint32
	EpisodeNumber uint32
	Overview      string
	Runtime       uint32
	AirDate       time.Time

	VoteAverage float32
	VoteCount   uint32
}

type TMDB struct {
	key     string
	baseUrl string
	http    *http.Client
}

// TODO: Return results from n+1 pages
func (t *TMDB) SearchMovies(title string, done <-chan struct{}, options map[string]string) (<-chan MovieSearchResult, <-chan error) {
	c := make(chan MovieSearchResult)
	errc := make(chan error, 1)
	errs := []error{}

	params := []string{
		fmt.Sprintf("query=%s", url.QueryEscape(title)),
		"include_adult=true",
	}
	for k, v := range options {
		params = append(params, fmt.Sprintf("%s=%s", k, url.QueryEscape(v)))
	}

	u := "https://api.themoviedb.org/3/search/movie" +
		fmt.Sprintf("?api_key=%s", t.key) +
		"&" + strings.Join(params, "&")

	var movies MovieSearchResults
	//fetch(u, &movies)
	res := make(chan error)
	requestc <- request{url: u, container: &movies, errc: res}
	if err := <-res; err != nil {
		errs = append(errs, err)
	}

	if len(movies.Results) == 0 {
		errs = append(errs, fmt.Errorf("no results found at tmdb for title: \"%s\" with options %v", title, options))
	}

	go func() {
		defer close(c)
		for _, r := range movies.Results {
			//fmt.Printf("before read: searched title %s with options %v; found %v", title, options, r)
			select {
			case c <- r:
				//fmt.Printf("searched title %s with options %v; found %v\n", title, options, r)
			case <-done:
				return
			}
		}
	}()
	errc <- errors.Join(errs...)
	return c, errc
}

func (t *TMDB) SearchMovie(title string, options map[string]string) (MovieSearchResult, error) {
	done := make(chan struct{})
	defer close(done)
	movies, errc := t.SearchMovies(title, done, options)
	if err := <-errc; err != nil {
		return MovieSearchResult{}, err
	}

	// TODO: match for more than 1 movie
	return <-movies, nil
}

func (t *TMDB) SearchTV(query string, done <-chan struct{}, options map[string]string) (<-chan TVSearchResult, <-chan error) {
	c := make(chan TVSearchResult)
	errc := make(chan error, 1)
	errs := []error{}

	params := []string{
		fmt.Sprintf("query=%s", url.QueryEscape(query)),
	}
	for k, v := range options {
		params = append(params, fmt.Sprintf("%s=%s", k, url.QueryEscape(v)))
	}

	u := "https://api.themoviedb.org/3/search/tv" +
		fmt.Sprintf("?api_key=%s", t.key) +
		"&" + strings.Join(params, "&")

	var series TVSearchResults
	res := make(chan error)
	requestc <- request{url: u, container: &series, errc: res}
	if err := <-res; err != nil {
		errs = append(errs, err)
	}

	if len(series.Results) == 0 {
		errs = append(errs, fmt.Errorf("no results found at tmdb for query: \"%s\" with options %v", query, options))
	}

	go func() {
		defer close(c)
		for _, r := range series.Results {
			select {
			case c <- r:
				//fmt.Printf("searched title %s with options %v; found %v\n", title, options, r)
			case <-done:
				return
			}
		}
	}()
	errc <- errors.Join(errs...)
	return c, errc
}

func (t *TMDB) SearchEpisode(series string, seriesYear int, season int, episode int) (EpisodeDetails, TVSearchResult, error) {
	done := make(chan struct{})
	defer close(done)

	var ep EpisodeDetails
	var searchopts map[string]string
	if seriesYear > 0 {
		searchopts = map[string]string{"year": fmt.Sprint(seriesYear)}
	}
	res, errc := t.SearchTV(series, done, searchopts)
	if err := <-errc; err != nil {
		return ep, TVSearchResult{}, err
	}
	show := <-res

	query := fmt.Sprintf("https://api.themoviedb.org/3/tv/%d/season/%d/episode/%d", show.ID, season, episode) +
		fmt.Sprintf("?api_key=%s", t.key)

	err := fetch(query, &ep)
	return ep, show, err
}

func New(k string) *TMDB {
	t := TMDB{
		key: k,
	}
	return &t
}

func fetch2(c <-chan request) {
	cache := map[string]any{}
	for r := range c {
		var err error
		if cached, ok := cache[r.url]; ok {
			// TODO: read up on this, lol. It works, but is mostly just copied after Googling
			// Naively assigning to the container without reflect doesn't update the value
			// at the caller
			ct := reflect.ValueOf(r.container).Elem()
			v := reflect.ValueOf(cached).Elem()
			ct.Set(v)
			r.errc <- nil
			close(r.errc)
			continue
		}

		req, _ := http.NewRequest("GET", r.url, nil)
		req.Header.Add("accept", "application/json")
		ctx := context.Background()
		limiter.Wait(ctx)

		var res *http.Response
		res, err = http.DefaultClient.Do(req)
		if err != nil {
			r.errc <- err
			close(r.errc)
			continue
		}
		// TODO: handle 429 responses
		if res.StatusCode == 429 {
			r.errc <- fmt.Errorf("rate limited (HTTP) 429")
			close(r.errc)
			continue
		}

		var body []byte
		body, err = io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			r.errc <- err
			close(r.errc)
			continue
		}

		if err := json.Unmarshal(body, r.container); err != nil {
			r.errc <- err
			close(r.errc)
			continue
		}

		cache[r.url] = r.container
		r.errc <- nil
		close(r.errc)
	}
}

func fetch(url string, dest any) error {
	var errs []error

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("accept", "application/json")
	ctx := context.Background()
	limiter.Wait(ctx)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		errs = append(errs, err)
	} else {
		defer res.Body.Close()
	}
	// This handling could be better, but backing off would need to be handled
	// in 1 synchronous place
	if res.StatusCode == 429 {
		errs = append(errs, fmt.Errorf("rate limited (HTTP) 429"))
		fmt.Println("rate limited.")
		return errors.Join(errs...)
	}

	var body []byte
	if b, err := io.ReadAll(res.Body); err != nil {
		errs = append(errs, err)
	} else {
		body = b
	}

	if err := json.Unmarshal(body, dest); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func init() {
	limiter = rate.NewLimiter(rate.Limit(40), 40) // was 40, 60
	requestc = make(chan request)
	go fetch2(requestc)
}
