package kourai

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

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

	// TODO: proper date
	ReleaseDate time.Time
	VoteAverage float32
	VoteCount   uint32
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

	searchUrl, _ := url.JoinPath(t.baseUrl, "3/search/movie")
	searchUrl += fmt.Sprintf("?api_key=%s", t.key) +
		"&" + strings.Join(params, "&")

	req, _ := http.NewRequest("GET", searchUrl, nil)
	req.Header.Add("accept", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		errs = append(errs, err)
	} else {
		defer res.Body.Close()
	}

	// check the status code res.StatusCode
	var body []byte
	if b, err := io.ReadAll(res.Body); err != nil {
		errs = append(errs, err)
	} else {
		body = b
	}
	//fmt.Println(string(body))

	var movies MovieSearchResults
	if err := json.Unmarshal(body, &movies); err != nil {
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

	query = "https://api.themoviedb.org/3/search/tv" +
		fmt.Sprintf("?api_key=%s", t.key) +
		"&" + strings.Join(params, "&")

	var series TVSearchResults
	err := fetch(query, &series)
	if err != nil {
		errs = append(errs, err)
	}
	if len(series.Results) == 0 {
		errs = append(errs, fmt.Errorf("no results found at tmdb for query: \"%s\" with options %v", query, options))
	}

	go func() {
		defer close(c)
		for _, r := range series.Results {
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

func (t *TMDB) SearchEpisode(series string, seriesYear int, season int, episode int) (EpisodeDetails, error) {
	done := make(chan struct{})
	defer close(done)

	var ep EpisodeDetails
	var searchopts map[string]string
	if seriesYear > 0 {
		searchopts = map[string]string{"year": fmt.Sprint(seriesYear)}
	}
	res, errc := t.SearchTV(series, done, searchopts)
	if err := <-errc; err != nil {
		return ep, err
	}
	show := <-res

	query := fmt.Sprintf("https://api.themoviedb.org/3/tv/%d/season/%d/episode/%d", show.ID, season, episode) +
		fmt.Sprintf("?api_key=%s", t.key)

	err := fetch(query, &ep)
	return ep, err
}

func New(k string) *TMDB {
	t := TMDB{
		key:     k,
		baseUrl: "https://api.themoviedb.org",
	}
	return &t
}

func fetch(url string, dest any) error {
	var errs []error
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("accept", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		errs = append(errs, err)
	} else {
		defer res.Body.Close()
	}

	// check the status code res.StatusCode
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
